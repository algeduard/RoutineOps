package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type AgentRelease struct {
	ID                string
	OS                string
	Arch              string
	Version           string
	Filename          string
	SHA256            string
	Signature         string // ed25519 над sha256(бинарь) — совместимость со старыми агентами
	ManifestSignature string // ed25519 над каноном version\nos\narch\nsha256 (SEC-3, anti-downgrade)
	CreatedAt         time.Time
}

func (db *DB) GetLatestAgentRelease(ctx context.Context, os, arch string) (*AgentRelease, error) {
	var r AgentRelease
	err := db.pool.QueryRow(ctx, `
		SELECT id, os, arch, version, filename, sha256, signature,
		       COALESCE(manifest_signature, ''), created_at
		FROM agent_releases
		WHERE os = $1 AND arch = $2
		ORDER BY created_at DESC LIMIT 1
	`, os, arch).Scan(&r.ID, &r.OS, &r.Arch, &r.Version, &r.Filename, &r.SHA256, &r.Signature, &r.ManifestSignature, &r.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

func (db *DB) RegisterAgentRelease(ctx context.Context, os, arch, version, filename, sha256, signature, manifestSignature string) error {
	// UPSERT по UNIQUE(os,arch,version): повторная публикация той же версии (повтор
	// update.sh, ретрай после сбоя сборки одной из платформ) — не падение на
	// unique-violation, а обновление артефакта/подписи (пересобранный бинарь той же
	// версии). created_at не трогаем — порядок «latest» стабилен.
	_, err := db.pool.Exec(ctx, `
		INSERT INTO agent_releases (os, arch, version, filename, sha256, signature, manifest_signature)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (os, arch, version) DO UPDATE SET
			filename           = EXCLUDED.filename,
			sha256             = EXCLUDED.sha256,
			signature          = EXCLUDED.signature,
			manifest_signature = EXCLUDED.manifest_signature
	`, os, arch, version, filename, sha256, signature, manifestSignature)
	return err
}

package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Каналы обновления агента (совпадают с CHECK-доменом в migrations/038 и с проверкой
// в API/publish-release). Пустая строка/неизвестное значение трактуются как stable
// (fail-safe: устройство/выборка без явного канала НИКОГДА не получит prerelease-билд).
const (
	ChannelStable = "stable"
	ChannelBeta   = "beta"
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
	Channel           string // 'stable'|'beta' (миграция 038); '' на выборке трактуется как stable
	CreatedAt         time.Time
}

// channelVisibility возвращает набор каналов релизов, которые может получить устройство
// на канале channel. beta видит beta+stable (в манифест уйдёт новейший из двух); всё
// остальное (в т.ч. пустое/неизвестное) схлопывается в stable-only — устройство с
// кривым каналом не должно внезапно поймать beta-билд.
func channelVisibility(channel string) []string {
	if channel == ChannelBeta {
		return []string{ChannelStable, ChannelBeta}
	}
	return []string{ChannelStable}
}

// GetLatestAgentReleaseForChannel — последний релиз для os/arch, ВИДИМЫЙ каналу channel.
// Ядро гейтинга self-update: stable-устройство фильтром отсекает beta, beta-устройство
// берёт новейший из stable+beta. nil (без ошибки) — релиза для этой пары нет.
func (db *DB) GetLatestAgentReleaseForChannel(ctx context.Context, os, arch, channel string) (*AgentRelease, error) {
	var r AgentRelease
	err := db.pool.QueryRow(ctx, `
		SELECT id, os, arch, version, filename, sha256, signature,
		       COALESCE(manifest_signature, ''), COALESCE(channel, 'stable'), created_at
		FROM agent_releases
		WHERE os = $1 AND arch = $2 AND channel = ANY($3)
		ORDER BY created_at DESC LIMIT 1
	`, os, arch, channelVisibility(channel)).Scan(&r.ID, &r.OS, &r.Arch, &r.Version, &r.Filename, &r.SHA256, &r.Signature, &r.ManifestSignature, &r.Channel, &r.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// GetLatestAgentRelease — последний СТАБИЛЬНЫЙ релиз для os/arch. До каналов возвращал
// абсолютно последний; теперь явно stable-only, чтобы установщик (getInstaller) и любой
// вызов без канала раздавали свежим/дефолтным устройствам stable, а не случайно beta.
// Обратная совместимость: до миграции 038 все релизы = stable, так что до первой
// публикации beta поведение не меняется.
func (db *DB) GetLatestAgentRelease(ctx context.Context, os, arch string) (*AgentRelease, error) {
	return db.GetLatestAgentReleaseForChannel(ctx, os, arch, ChannelStable)
}

func (db *DB) RegisterAgentRelease(ctx context.Context, os, arch, version, filename, sha256, signature, manifestSignature, channel string) error {
	if channel == "" {
		channel = ChannelStable
	}
	// UPSERT по UNIQUE(os,arch,version): повторная публикация той же версии (повтор
	// update.sh, ретрай после сбоя сборки одной из платформ) — не падение на
	// unique-violation, а обновление артефакта/подписи/канала (пересобранный бинарь той
	// же версии, либо «промоушен» beta→stable переопубликацией). created_at не трогаем —
	// порядок «latest» стабилен.
	_, err := db.pool.Exec(ctx, `
		INSERT INTO agent_releases (os, arch, version, filename, sha256, signature, manifest_signature, channel)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (os, arch, version) DO UPDATE SET
			filename           = EXCLUDED.filename,
			sha256             = EXCLUDED.sha256,
			signature          = EXCLUDED.signature,
			manifest_signature = EXCLUDED.manifest_signature,
			channel            = EXCLUDED.channel
	`, os, arch, version, filename, sha256, signature, manifestSignature, channel)
	return err
}

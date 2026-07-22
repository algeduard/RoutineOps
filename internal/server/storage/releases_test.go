package storage_test

import (
	"context"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// Повторная публикация той же (os,arch,version) должна быть идемпотентной (UPSERT),
// а не падать на UNIQUE(os,arch,version): повтор update.sh / ретрай после сбоя сборки
// одной из платформ не должен ронять весь публиш. Артефакт/подписи при этом обновляются
// (пересобранный бинарь той же версии).
func TestRegisterAgentRelease_Idempotent(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	osName := "testos" + uniq(t) // уникальный os → GetLatest вернёт только нашу строку
	arch := "amd64"
	ver := "v1.0.0"

	if err := db.RegisterAgentRelease(ctx, osName, arch, ver, "agent_old", "sha_old", "sig_old", "msig_old", storage.ChannelStable); err != nil {
		t.Fatalf("первый register: %v", err)
	}
	// та же версия, новый артефакт — НЕ ошибка
	if err := db.RegisterAgentRelease(ctx, osName, arch, ver, "agent_new", "sha_new", "sig_new", "msig_new", storage.ChannelStable); err != nil {
		t.Fatalf("повторный register той же версии обязан быть идемпотентным, получили: %v", err)
	}

	rel, err := db.GetLatestAgentRelease(ctx, osName, arch)
	if err != nil {
		t.Fatalf("GetLatestAgentRelease: %v", err)
	}
	if rel == nil {
		t.Fatal("релиз не найден после register")
	}
	if rel.Version != ver {
		t.Fatalf("version = %q, ожидали %q", rel.Version, ver)
	}
	// UPSERT обновил артефакт/подпись, не создал дубль
	if rel.Filename != "agent_new" || rel.SHA256 != "sha_new" ||
		rel.Signature != "sig_new" || rel.ManifestSignature != "msig_new" {
		t.Fatalf("UPSERT не обновил артефакт: filename=%q sha=%q sig=%q msig=%q",
			rel.Filename, rel.SHA256, rel.Signature, rel.ManifestSignature)
	}
	if rel.Channel != storage.ChannelStable {
		t.Fatalf("channel = %q, ожидали stable", rel.Channel)
	}
}

// Пустой канал в RegisterAgentRelease нормализуется в stable — старые вызовы/скрипты,
// не знающие про каналы, не должны писать пустую строку в NOT NULL-колонку.
func TestRegisterAgentRelease_EmptyChannelDefaultsStable(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	osName := "testos" + uniq(t)

	if err := db.RegisterAgentRelease(ctx, osName, "amd64", "v1.0.0", "f", "s", "sig", "msig", ""); err != nil {
		t.Fatalf("register с пустым каналом: %v", err)
	}
	rel, err := db.GetLatestAgentRelease(ctx, osName, "amd64")
	if err != nil || rel == nil {
		t.Fatalf("GetLatestAgentRelease: rel=%v err=%v", rel, err)
	}
	if rel.Channel != storage.ChannelStable {
		t.Fatalf("пустой канал должен стать stable, получили %q", rel.Channel)
	}
}

// Ядро гейтинга: stable-устройство НИКОГДА не видит beta, а beta-устройство видит
// новейший из stable+beta. Публикуем stable v1.0.0, затем beta v1.1.0 (позже по
// created_at) — stable-канал остаётся на v1.0.0, beta-канал уходит на v1.1.0.
func TestGetLatestAgentReleaseForChannel_Gating(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	osName := "testos" + uniq(t)
	arch := "amd64"

	if err := db.RegisterAgentRelease(ctx, osName, arch, "v1.0.0", "f_stable", "sha_s", "sig", "msig", storage.ChannelStable); err != nil {
		t.Fatalf("register stable: %v", err)
	}
	if err := db.RegisterAgentRelease(ctx, osName, arch, "v1.1.0", "f_beta", "sha_b", "sig", "msig", storage.ChannelBeta); err != nil {
		t.Fatalf("register beta: %v", err)
	}

	stable, err := db.GetLatestAgentReleaseForChannel(ctx, osName, arch, storage.ChannelStable)
	if err != nil || stable == nil {
		t.Fatalf("stable канал: rel=%v err=%v", stable, err)
	}
	if stable.Version != "v1.0.0" {
		t.Fatalf("stable-устройство увидело %q, ожидали v1.0.0 (beta должна быть скрыта)", stable.Version)
	}

	beta, err := db.GetLatestAgentReleaseForChannel(ctx, osName, arch, storage.ChannelBeta)
	if err != nil || beta == nil {
		t.Fatalf("beta канал: rel=%v err=%v", beta, err)
	}
	if beta.Version != "v1.1.0" {
		t.Fatalf("beta-устройство увидело %q, ожидали v1.1.0", beta.Version)
	}

	// Неизвестный/пустой канал схлопывается в stable (fail-safe).
	fallback, err := db.GetLatestAgentReleaseForChannel(ctx, osName, arch, "garbage")
	if err != nil || fallback == nil {
		t.Fatalf("неизвестный канал: rel=%v err=%v", fallback, err)
	}
	if fallback.Version != "v1.0.0" {
		t.Fatalf("неизвестный канал увидел %q, ожидали stable v1.0.0", fallback.Version)
	}
}

// Если beta-релиз НЕ новее последнего stable — beta-устройство берёт stable: канал
// «beta видит beta+stable, новейший из двух», а не «beta любой ценой».
func TestGetLatestAgentReleaseForChannel_BetaTakesNewerStable(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	osName := "testos" + uniq(t)
	arch := "amd64"

	// Порядок вставки = порядок created_at: сначала beta, потом более новый stable.
	if err := db.RegisterAgentRelease(ctx, osName, arch, "v1.0.0-beta", "f_beta", "sha_b", "sig", "msig", storage.ChannelBeta); err != nil {
		t.Fatalf("register beta: %v", err)
	}
	if err := db.RegisterAgentRelease(ctx, osName, arch, "v1.0.0", "f_stable", "sha_s", "sig", "msig", storage.ChannelStable); err != nil {
		t.Fatalf("register stable: %v", err)
	}

	beta, err := db.GetLatestAgentReleaseForChannel(ctx, osName, arch, storage.ChannelBeta)
	if err != nil || beta == nil {
		t.Fatalf("beta канал: rel=%v err=%v", beta, err)
	}
	if beta.Channel != storage.ChannelStable {
		t.Fatalf("beta-устройство должно взять новейший stable, получили канал %q версия %q", beta.Channel, beta.Version)
	}
}

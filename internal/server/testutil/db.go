package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// NewDSNWithCleanup возвращает DSN и функцию очистки — для использования в TestMain.
// Если установлена TEST_POSTGRES_DSN (admin DSN), создаёт уникальную БД и возвращает
// DSN на неё; cleanup дропает БД. Это даёт изоляцию между пакетами при общем сервере.
func NewDSNWithCleanup() (dsn string, cleanup func()) {
	if adminDSN := os.Getenv("TEST_POSTGRES_DSN"); adminDSN != "" {
		dsn, drop := createTempDatabase(adminDSN)
		runMigrationsCtx(context.Background(), dsn)
		return dsn, drop
	}

	ctx := context.Background()
	c, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("mdm_test"),
		postgres.WithUsername("mdm"),
		postgres.WithPassword("mdm"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		panic("testcontainers postgres: " + err.Error())
	}
	dsn, err = c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic("connection string: " + err.Error())
	}
	runMigrationsCtx(ctx, dsn)
	return dsn, func() { _ = c.Terminate(ctx) }
}

// createTempDatabase создаёт уникальную БД на сервере, указанном в adminDSN,
// и возвращает DSN на неё + функцию-дропалку.
func createTempDatabase(adminDSN string) (dsn string, drop func()) {
	ctx := context.Background()
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		panic("rand: " + err.Error())
	}
	dbName := "mdm_test_" + hex.EncodeToString(buf)

	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		panic("admin connect: " + err.Error())
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", dbName)); err != nil {
		_ = conn.Close(ctx)
		panic("CREATE DATABASE: " + err.Error())
	}
	_ = conn.Close(ctx)

	u, err := url.Parse(adminDSN)
	if err != nil {
		panic("parse adminDSN: " + err.Error())
	}
	u.Path = "/" + dbName
	dsn = u.String()

	drop = func() {
		c, err := pgx.Connect(ctx, adminDSN)
		if err != nil {
			return
		}
		defer c.Close(ctx)
		_, _ = c.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", dbName))
	}
	return dsn, drop
}

func runMigrationsCtx(ctx context.Context, dsn string) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		panic("connect for migrations: " + err.Error())
	}
	defer conn.Close(ctx)

	root := findRoot()
	entries, _ := os.ReadDir(filepath.Join(root, "migrations"))
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, filepath.Join(root, "migrations", e.Name()))
		}
	}
	sort.Strings(files)
	for _, f := range files {
		sql, _ := os.ReadFile(f)
		if _, err := conn.Exec(ctx, string(sql)); err != nil {
			panic("migration " + filepath.Base(f) + ": " + err.Error())
		}
	}
}

func findRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("go.mod not found")
		}
		dir = parent
	}
}

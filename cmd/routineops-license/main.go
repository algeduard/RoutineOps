//go:build enterprise

// routineops-license — вендор-тулинг лицензий (enterprise-сборка). Владелец форка = центр
// лицензирования: keygen выпускает пару ключей, issue подписывает лицензии приватным
// ключом, inspect проверяет/показывает. Публичный ключ вкомпилируется в сервер
// (-ldflags ...defaultPubKeyB64=<pub>) или задаётся ROUTINEOPS_LICENSE_PUBKEY.
//
// Free-сборка содержит только main_free.go (//go:build !enterprise) — заглушку.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Floodww/RoutineOps/internal/license"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = cmdKeygen(os.Args[2:])
	case "issue":
		err = cmdIssue(os.Args[2:])
	case "inspect":
		err = cmdInspect(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "неизвестная команда %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `routineops-license — выпуск и проверка лицензий RoutineOps Enterprise

  keygen  [-out FILE]                 сгенерировать пару ed25519; публичный → stdout,
                                      приватный → FILE (0600) или stdout
  issue   -key FILE [опции]           выпустить подписанную лицензию (blob → stdout)
  inspect [-in FILE] [-pubkey B64]    декодировать/проверить лицензию

issue-опции:
  -licensee NAME    кому выдана
  -edition NAME     редакция (по умолчанию "enterprise")
  -features CSV     список фич через запятую (пусто = вся редакция)
  -seats N          устройств по договору (0 = без лимита)
  -days N           срок с текущего момента, дней (0 = бессрочно)
  -expires RFC3339  точная дата окончания (перекрывает -days)
  -not-before RFC3339  дата начала действия (по умолчанию сейчас)
  -password PW      пароль активации (пусто = без пароля)
  -id STR           license_id (по умолчанию случайный)
`)
}

func cmdKeygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	out := fs.String("out", "", "файл для приватного ключа (0600); пусто = stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	privB64 := base64.StdEncoding.EncodeToString(priv)

	fmt.Println("публичный ключ (base64) — для сборки сервера:")
	fmt.Println("  go build -tags enterprise -ldflags \"-X github.com/Floodww/RoutineOps/internal/license.defaultPubKeyB64=" + pubB64 + "\" ./cmd/server")
	fmt.Println("или ROUTINEOPS_LICENSE_PUBKEY=" + pubB64)
	fmt.Println()
	if *out != "" {
		// 0600: приватный ключ — секрет; создаём эксклюзивно, чтобы не затереть существующий.
		f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("запись ключа: %w", err)
		}
		if _, err := f.WriteString(privB64 + "\n"); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "приватный ключ записан в %s (держите в секрете; им подписываются лицензии)\n", *out)
	} else {
		fmt.Println("приватный ключ (base64) — ДЕРЖИТЕ В СЕКРЕТЕ, для `issue -key`:")
		fmt.Println("  " + privB64)
	}
	return nil
}

func cmdIssue(args []string) error {
	fs := flag.NewFlagSet("issue", flag.ContinueOnError)
	keyFile := fs.String("key", "", "файл приватного ключа (обязательно)")
	licensee := fs.String("licensee", "", "кому выдана")
	edition := fs.String("edition", "enterprise", "редакция")
	features := fs.String("features", "", "фичи через запятую (пусто = вся редакция)")
	seats := fs.Int("seats", 0, "устройств по договору (0 = без лимита)")
	days := fs.Int("days", 0, "срок с текущего момента, дней (0 = бессрочно)")
	expires := fs.String("expires", "", "дата окончания RFC3339 (перекрывает -days)")
	notBefore := fs.String("not-before", "", "дата начала RFC3339 (по умолчанию сейчас)")
	password := fs.String("password", "", "пароль активации (пусто = без пароля)")
	id := fs.String("id", "", "license_id (по умолчанию случайный)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyFile == "" {
		return fmt.Errorf("-key обязателен (файл приватного ключа из keygen)")
	}
	priv, err := readPrivKey(*keyFile)
	if err != nil {
		return err
	}

	now := time.Now().UTC().Truncate(time.Second)
	c := license.Claims{
		Licensee: *licensee,
		Edition:  *edition,
		Seats:    *seats,
		IssuedAt: now,
		LicenseID: func() string {
			if *id != "" {
				return *id
			}
			return randomID()
		}(),
	}
	if *features != "" {
		for _, f := range strings.Split(*features, ",") {
			if f = strings.TrimSpace(f); f != "" {
				c.Features = append(c.Features, f)
			}
		}
	}
	if *notBefore != "" {
		c.NotBefore, err = time.Parse(time.RFC3339, *notBefore)
		if err != nil {
			return fmt.Errorf("-not-before: %w", err)
		}
	} else {
		c.NotBefore = now
	}
	switch {
	case *expires != "":
		c.ExpiresAt, err = time.Parse(time.RFC3339, *expires)
		if err != nil {
			return fmt.Errorf("-expires: %w", err)
		}
	case *days > 0:
		c.ExpiresAt = now.Add(time.Duration(*days) * 24 * time.Hour)
	}
	if *password != "" {
		salt, hash, err := license.HashPassword(*password)
		if err != nil {
			return err
		}
		c.PwSalt, c.PwHash = salt, hash
	}

	blob, err := license.Issue(c, priv)
	if err != nil {
		return err
	}
	fmt.Println(blob)
	return nil
}

func cmdInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	in := fs.String("in", "", "файл с blob (по умолчанию stdin)")
	pubB64 := fs.String("pubkey", "", "публичный ключ base64 для проверки подписи/срока")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var blob string
	if *in != "" {
		b, err := os.ReadFile(*in)
		if err != nil {
			return err
		}
		blob = strings.TrimSpace(string(b))
	} else {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		blob = strings.TrimSpace(string(b))
	}

	if *pubB64 != "" {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(*pubB64))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return fmt.Errorf("-pubkey: неверный ключ")
		}
		lic, err := license.Parse(blob, ed25519.PublicKey(raw))
		if err != nil {
			return fmt.Errorf("проверка подписи: %w", err)
		}
		printClaims(lic.Claims, true)
		if err := lic.ValidAt(time.Now(), 0); err != nil {
			fmt.Println("срок:      НЕ действует —", err)
		} else {
			fmt.Println("срок:      действует")
		}
		return nil
	}

	// Без ключа — только декодируем (подпись НЕ проверена).
	c, err := decodeUnverified(blob)
	if err != nil {
		return err
	}
	printClaims(c, false)
	return nil
}

// decodeUnverified достаёт Claims без проверки подписи — только для inspect без ключа.
func decodeUnverified(blob string) (license.Claims, error) {
	outer, err := base64.StdEncoding.DecodeString(strings.TrimSpace(blob))
	if err != nil {
		return license.Claims{}, license.ErrMalformed
	}
	var w struct {
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(outer, &w); err != nil {
		return license.Claims{}, license.ErrMalformed
	}
	payload, err := base64.StdEncoding.DecodeString(w.Payload)
	if err != nil {
		return license.Claims{}, license.ErrMalformed
	}
	var c license.Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return license.Claims{}, license.ErrMalformed
	}
	return c, nil
}

func printClaims(c license.Claims, verified bool) {
	if verified {
		fmt.Println("подпись:   ВЕРНА")
	} else {
		fmt.Println("подпись:   НЕ ПРОВЕРЕНА (ключ не задан)")
	}
	fmt.Println("licensee:  ", orDash(c.Licensee))
	fmt.Println("edition:   ", orDash(c.Edition))
	if len(c.Features) == 0 {
		fmt.Println("features:   вся редакция")
	} else {
		fmt.Println("features:  ", strings.Join(c.Features, ", "))
	}
	fmt.Println("seats:     ", c.Seats)
	fmt.Println("license_id:", orDash(c.LicenseID))
	fmt.Println("not_before:", tsOrDash(c.NotBefore))
	fmt.Println("expires_at:", tsOrDash(c.ExpiresAt))
	fmt.Println("пароль:    ", c.PwHash != "")
}

func readPrivKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("чтение ключа: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("ключ не base64: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("ключ неверного размера %d (ожидался %d)", len(raw), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(raw), nil
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "lic-" + hex.EncodeToString(b)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func tsOrDash(t time.Time) string {
	if t.IsZero() {
		return "— (без ограничения)"
	}
	return t.Format(time.RFC3339)
}

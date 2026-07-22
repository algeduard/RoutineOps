package helpreq

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Заявка из окна помощи сериализуется в файл (скриншот — base64 внутри JSON)
// и читается службой обратно без потерь.
func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "help-request.json")
	shot := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x01, 0x02} // JPEG-подобные байты
	if err := Write(path, UserRequest{Message: "не печатает принтер", ScreenshotJPEG: shot, Reporter: `OFFICE\ivanov`}); err != nil {
		t.Fatal(err)
	}
	r, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if r.Message != "не печатает принтер" || r.Reporter != `OFFICE\ivanov` || r.CreatedAt == 0 {
		t.Fatalf("заявка прочитана неверно: %+v", r)
	}
	if !bytes.Equal(r.ScreenshotJPEG, shot) {
		t.Fatalf("скриншот повреждён: %v", r.ScreenshotJPEG)
	}
}

// Write атомарен: tmp-файла после записи не остаётся.
func TestWriteLeavesNoTmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "help-request.json")
	if err := Write(path, UserRequest{Message: "тест"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tmp-файл остался после записи: %v", err)
	}
}

// Нет файла-заявки → os.ErrNotExist (служба трактует как «обращений нет»).
func TestReadMissing(t *testing.T) {
	_, err := Read(filepath.Join(t.TempDir(), "нет.json"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ожидали ErrNotExist, got %v", err)
	}
}

func TestIsTerminalSubmitErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"NotFound", status.Error(codes.NotFound, "device not found"), true},
		{"InvalidArgument", status.Error(codes.InvalidArgument, "screenshot too large"), true},
		{"FailedPrecondition", status.Error(codes.FailedPrecondition, "precondition"), true},
		{"ResourceExhausted (кулдаун → транзиент)", status.Error(codes.ResourceExhausted, "cooldown"), false},
		{"Unavailable (транзиент)", status.Error(codes.Unavailable, "down"), false},
		{"plain error (dial fail → Unknown)", io.EOF, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTerminalSubmitErr(tt.err); got != tt.want {
				t.Errorf("isTerminalSubmitErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// processRequest: успех и терминальный отказ снимают файл-заявку, транзиент
// (включая серверный кулдаун) — оставляет для повтора следующим тиком.
func TestProcessRequest(t *testing.T) {
	tests := []struct {
		name      string
		sendErr   error
		wantSent  bool
		fileStays bool
	}{
		{"success → файл снят", nil, true, false},
		{"терминальный → файл снят", status.Error(codes.InvalidArgument, "bad"), true, false},
		{"кулдаун → файл оставлен", status.Error(codes.ResourceExhausted, "cooldown"), true, true},
		{"транзиент → файл оставлен", status.Error(codes.Unavailable, "down"), true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "help-request.json")
			if err := Write(path, UserRequest{Message: "помогите"}); err != nil {
				t.Fatalf("Write: %v", err)
			}
			var sent bool
			send := func(r UserRequest) error {
				sent = true
				if r.Message != "помогите" {
					t.Errorf("send получил message=%q", r.Message)
				}
				return tt.sendErr
			}

			processRequest(path, send, discardLog())

			if sent != tt.wantSent {
				t.Errorf("send вызван=%v, want %v", sent, tt.wantSent)
			}
			_, statErr := Read(path)
			fileStays := statErr == nil
			if fileStays != tt.fileStays {
				t.Errorf("файл остался=%v, want %v", fileStays, tt.fileStays)
			}
		})
	}
}

// Нет файла-заявки → send не вызывается (служба молча ждёт следующего тика).
func TestProcessRequestNoFile(t *testing.T) {
	var sent bool
	processRequest(filepath.Join(t.TempDir(), "missing.json"),
		func(UserRequest) error { sent = true; return nil }, discardLog())
	if sent {
		t.Error("send не должен вызываться при отсутствии файла-заявки")
	}
}

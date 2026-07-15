package lock

import (
	"context"
	"errors"
	"testing"

	pb "github.com/Floodww/RoutineOps/proto"
)

// fakeFileVaultRevoker записывает вызовы RevokeAndShutdown (pull-путь).
type fakeFileVaultRevoker struct {
	calls []string
	state pb.LockState
	err   error
}

func (f *fakeFileVaultRevoker) RevokeAndShutdown(_ context.Context, requestID string) (pb.LockState, error) {
	f.calls = append(f.calls, requestID)
	return f.state, f.err
}

func newTestReconciler(mgr *Manager, fetchResp *pb.FetchLockStatusResponse, fetchErr error) *Reconciler {
	return &Reconciler{
		mgr:      mgr,
		interval: 0,
		log:      quietLog(),
		fetch: func(context.Context) (*pb.FetchLockStatusResponse, error) {
			return fetchResp, fetchErr
		},
		report: func(context.Context, *pb.ReportLockStatusRequest) error { return nil },
	}
}

// desired lock_mode=FILEVAULT → реконсиляция дёргает revoker, НЕ mgr.Lock
// (overlay lock.json/оверлей не должны трогаться для FileVault-режима).
func TestReconcile_FileVaultMode_CallsRevoker_NotOverlay(t *testing.T) {
	fl := &fakeLocker{}
	mgr := newMgr(t, fl)
	fr := &fakeFileVaultRevoker{state: pb.LockState_LOCK_STATE_FILEVAULT_REVOKED}

	r := newTestReconciler(mgr, &pb.FetchLockStatusResponse{
		Locked: true, PasswordHash: "hash-fv", LockMode: pb.LockMode_LOCK_MODE_FILEVAULT,
	}, nil)
	r.SetFileVaultRevoker(fr)

	r.tick(context.Background())

	if len(fr.calls) != 1 || fr.calls[0] != "hash-fv" {
		t.Fatalf("expected RevokeAndShutdown called once with hash-fv, got %v", fr.calls)
	}
	if fl.shows != 0 {
		t.Fatalf("overlay locker must not be shown for lock_mode=FILEVAULT, got shows=%d", fl.shows)
	}
	if mgr.Locked() {
		t.Fatalf("lock.Manager (overlay state) must remain untouched for FILEVAULT mode")
	}
}

// desired lock_mode=OVERLAY (или unspecified) → обычный путь через mgr.Lock,
// revoker не трогается, даже если сконфигурирован.
func TestReconcile_OverlayMode_DoesNotCallRevoker(t *testing.T) {
	fl := &fakeLocker{}
	mgr := newMgr(t, fl)
	fr := &fakeFileVaultRevoker{state: pb.LockState_LOCK_STATE_FILEVAULT_REVOKED}

	r := newTestReconciler(mgr, &pb.FetchLockStatusResponse{
		Locked: true, PasswordHash: "hash-overlay",
	}, nil)
	r.SetFileVaultRevoker(fr)

	r.tick(context.Background())

	if len(fr.calls) != 0 {
		t.Fatalf("revoker must not be called for overlay/unspecified lock_mode, got %v", fr.calls)
	}
	if !mgr.Locked() {
		t.Fatalf("expected overlay lock to be applied")
	}
}

// Без revoker lock_mode=FILEVAULT логируется как ошибка, mgr.Lock НЕ
// вызывается (нет тихой деградации в overlay).
func TestReconcile_FileVaultMode_NoRevoker_DoesNotFallBackToOverlay(t *testing.T) {
	fl := &fakeLocker{}
	mgr := newMgr(t, fl)

	r := newTestReconciler(mgr, &pb.FetchLockStatusResponse{
		Locked: true, PasswordHash: "hash-fv", LockMode: pb.LockMode_LOCK_MODE_FILEVAULT,
	}, nil) // SetFileVaultRevoker не вызывали

	r.tick(context.Background())

	if mgr.Locked() {
		t.Fatalf("must not silently degrade to overlay lock when revoker is unconfigured")
	}
}

// Ошибка RevokeAndShutdown (ABORT одного из гардов) логируется, оверлей не трогается.
func TestReconcile_FileVaultMode_RevokeError_DoesNotTouchOverlay(t *testing.T) {
	fl := &fakeLocker{}
	mgr := newMgr(t, fl)
	fr := &fakeFileVaultRevoker{err: errors.New("revoke ABORT — residual owner")}

	r := newTestReconciler(mgr, &pb.FetchLockStatusResponse{
		Locked: true, PasswordHash: "hash-fv", LockMode: pb.LockMode_LOCK_MODE_FILEVAULT,
	}, nil)
	r.SetFileVaultRevoker(fr)

	r.tick(context.Background())

	if mgr.Locked() {
		t.Fatalf("overlay must remain untouched even when FileVault revoke fails")
	}
}

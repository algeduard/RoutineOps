package admin

import (
	"testing"

	pb "github.com/Floodww/RoutineOps/proto"
)

func sw(name, ver string) *pb.SoftwareItem {
	return &pb.SoftwareItem{SoftwareName: name, Version: ver}
}

func names(items []*pb.SoftwareItem) map[string]string {
	m := map[string]string{}
	for _, s := range items {
		m[s.GetSoftwareName()] = s.GetVersion()
	}
	return m
}

func TestDiffSoftware(t *testing.T) {
	baseline := []*pb.SoftwareItem{sw("Chrome", "120"), sw("VLC", "3.0"), sw("7-Zip", "22")}
	// За сессию: поставили Wireshark, удалили VLC, обновили Chrome 120→121 (= удаление 120 + установка 121).
	current := []*pb.SoftwareItem{sw("Chrome", "121"), sw("7-Zip", "22"), sw("Wireshark", "4.2")}

	added, removed := diffSoftware(baseline, current)

	a := names(added)
	if a["Wireshark"] != "4.2" || a["Chrome"] != "121" || len(a) != 2 {
		t.Errorf("added = %v, ожидали Wireshark 4.2 + Chrome 121", a)
	}
	r := names(removed)
	if r["VLC"] != "3.0" || r["Chrome"] != "120" || len(r) != 2 {
		t.Errorf("removed = %v, ожидали VLC 3.0 + Chrome 120", r)
	}
}

func TestDiffSoftware_NoChanges(t *testing.T) {
	list := []*pb.SoftwareItem{sw("A", "1"), sw("B", "2")}
	added, removed := diffSoftware(list, list)
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("без изменений ожидали пусто, got added=%v removed=%v", added, removed)
	}
}

func TestDiffSoftware_NilBaseline(t *testing.T) {
	// Нет базового снимка (напр. рестарт агента посреди сессии) → дельта пустая.
	added, removed := diffSoftware(nil, []*pb.SoftwareItem{sw("A", "1")})
	if added != nil || removed != nil {
		t.Errorf("при nil baseline ожидали nil,nil, got added=%v removed=%v", added, removed)
	}
}

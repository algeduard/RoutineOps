package inventory

import (
	"testing"

	pb "github.com/Floodww/RoutineOps/proto"
)

func report(ram int64, sw ...*pb.SoftwareItem) *pb.InventoryReport {
	return &pb.InventoryReport{
		DeviceInfo: &pb.DeviceInfo{Hostname: "h", Os: "macOS", Ram: ram},
		Software:   sw,
	}
}

func TestHashReportStable(t *testing.T) {
	r := report(16, &pb.SoftwareItem{SoftwareName: "a", Version: "1"})
	h1, h2 := hashReport(r), hashReport(r)
	if h1 != h2 {
		t.Fatal("хэш нестабилен на одном входе")
	}
}

func TestHashReportOrderIndependent(t *testing.T) {
	a := &pb.SoftwareItem{SoftwareName: "a", Version: "1"}
	b := &pb.SoftwareItem{SoftwareName: "b", Version: "2"}
	if hashReport(report(16, a, b)) != hashReport(report(16, b, a)) {
		t.Fatal("порядок ПО не должен влиять на хэш")
	}
}

func TestHashReportChangesOnDiff(t *testing.T) {
	base := report(16, &pb.SoftwareItem{SoftwareName: "a", Version: "1"})
	if hashReport(base) == hashReport(report(32, &pb.SoftwareItem{SoftwareName: "a", Version: "1"})) {
		t.Fatal("изменение RAM должно менять хэш")
	}
	if hashReport(base) == hashReport(report(16, &pb.SoftwareItem{SoftwareName: "a", Version: "2"})) {
		t.Fatal("изменение версии ПО должно менять хэш")
	}
}

// После self-update версия агента меняется даже если прочий снимок тот же —
// хэш обязан отличаться, иначе новая версия не доедет до сервера.
func TestHashReportChangesOnAgentVersion(t *testing.T) {
	base := report(16)
	bumped := report(16)
	bumped.DeviceInfo.AgentVersion = "2.0.0"
	if hashReport(base) == hashReport(bumped) {
		t.Fatal("изменение версии агента должно менять хэш")
	}
}

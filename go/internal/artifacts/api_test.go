package artifacts

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestNodeMaximumStringLengthMatchesNode2415(t *testing.T) {
	if os.Getenv("INFRAWRIGHT_FROZEN_NODE_ORACLE") == "" {
		t.Skip("archived runtime oracle is opt-in")
	}
	if !boundedFilePlatformSupported {
		t.Skip("Node MAX_STRING_LENGTH oracle applies only to supported 64-bit bounded-file targets")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("Node v24.15.0 MAX_STRING_LENGTH oracle unavailable: exec.LookPath(node) error = %v", err)
	}
	version, err := exec.Command(node, "--version").Output()
	if err != nil {
		t.Skipf("Node v24.15.0 MAX_STRING_LENGTH oracle unavailable: node --version error = %v", err)
	}
	if got := strings.TrimSpace(string(version)); got != "v24.15.0" {
		t.Skipf("Node MAX_STRING_LENGTH oracle requires v24.15.0, got %q", got)
	}

	got := nodeMaximumStringLengthOracle(t, node, "Node v24.15.0")
	if got != nodeMaximumStringLength {
		t.Errorf("Node v24.15.0 buffer.constants.MAX_STRING_LENGTH = %d, want frozen Go literal %d", got, nodeMaximumStringLength)
	}
}

func TestNode24MaximumStringLengthRemainsCompatible(t *testing.T) {
	if os.Getenv("INFRAWRIGHT_FROZEN_NODE_ORACLE") == "" {
		t.Skip("archived runtime oracle is opt-in")
	}
	if !boundedFilePlatformSupported {
		t.Skip("Node MAX_STRING_LENGTH oracle applies only to supported 64-bit bounded-file targets")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("Node 24 MAX_STRING_LENGTH compatibility oracle unavailable in CI: exec.LookPath(node) error = %v", err)
		}
		t.Skipf("Node 24 MAX_STRING_LENGTH compatibility oracle unavailable: exec.LookPath(node) error = %v", err)
	}
	versionOutput, err := exec.Command(node, "--version").Output()
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("Node 24 MAX_STRING_LENGTH compatibility oracle unavailable in CI: node --version error = %v", err)
		}
		t.Skipf("Node 24 MAX_STRING_LENGTH compatibility oracle unavailable: node --version error = %v", err)
	}
	version := strings.TrimSpace(string(versionOutput))
	if !strings.HasPrefix(version, "v24.") {
		if os.Getenv("CI") != "" {
			t.Fatalf("Node MAX_STRING_LENGTH compatibility oracle requires Node 24 in CI, got %q", version)
		}
		t.Skipf("Node MAX_STRING_LENGTH compatibility oracle requires Node 24, got %q", version)
	}

	got := nodeMaximumStringLengthOracle(t, node, "Node "+version)
	if got != nodeMaximumStringLength {
		t.Errorf("Node %s buffer.constants.MAX_STRING_LENGTH = %d, want frozen Go literal %d", version, got, nodeMaximumStringLength)
	}
}

func nodeMaximumStringLengthOracle(t *testing.T, node, label string) int64 {
	t.Helper()
	output, err := exec.Command(
		node,
		"-e",
		`console.log(require("buffer").constants.MAX_STRING_LENGTH)`,
	).Output()
	if err != nil {
		t.Fatalf("%s MAX_STRING_LENGTH oracle error = %v, want nil", label, err)
	}
	oracle := strings.TrimSpace(string(output))
	got, err := strconv.ParseInt(oracle, 10, 64)
	if err != nil {
		t.Fatalf("%s MAX_STRING_LENGTH oracle output %q parse error = %v, want a base-10 int64", label, oracle, err)
	}
	return got
}

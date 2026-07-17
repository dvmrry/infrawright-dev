package resthttp

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

func TestPinnedNode2415BundledRootAuthority(t *testing.T) {
	certificates, err := nodeBundledRoots()
	if err != nil {
		t.Fatalf("nodeBundledRoots() failed: %v", err)
	}
	if len(certificates) != 145 {
		t.Fatalf("bundled roots = %d, want 145", len(certificates))
	}
	ders := make([][]byte, 0, len(certificates))
	total := 0
	for _, certificate := range certificates {
		ders = append(ders, certificate.Raw)
		total += len(certificate.Raw)
	}
	if total != 154758 {
		t.Errorf("bundled root DER bytes = %d, want 154758", total)
	}
	orderedDigest, setDigest := nodeRootDigests(ders)
	if orderedDigest != "1ca141cc18a277855d509ea58ac4a2eeb3d079344e3496d50557e6a1609634d3" {
		t.Errorf("ordered root digest = %s", orderedDigest)
	}
	if setDigest != "58dd2d2c5037f7d9f5b0b2d182113230381e81f624162360dd99eaaea25dfc7c" {
		t.Errorf("set root digest = %s", setDigest)
	}
	first := fmt.Sprintf("%x", sha256.Sum256(certificates[0].Raw))
	last := fmt.Sprintf("%x", sha256.Sum256(certificates[len(certificates)-1].Raw))
	if first != "73c176434f1bc6d5adf45b0e76e727287c8de57616c1e6e6141a2b2cbc7d8e4c" {
		t.Errorf("first root digest = %s", first)
	}
	if last != "b49141502d00663d740f2e7ec340c52800962666121a36d09cf7dd2b90384fb4" {
		t.Errorf("last root digest = %s", last)
	}

	pool, err := trustedCertificates(collectors.Environment{}, false)
	if err != nil {
		t.Fatalf("trustedCertificates() failed: %v", err)
	}
	if got := len(pool.Subjects()); got != 145 {
		t.Errorf("default trust-pool subjects = %d, want exactly 145", got)
	}
}

func TestNodeBundledRootsReturnsDefensiveCopies(t *testing.T) {
	first, err := nodeBundledRoots()
	if err != nil {
		t.Fatalf("nodeBundledRoots() first call failed: %v", err)
	}
	second, err := nodeBundledRoots()
	if err != nil {
		t.Fatalf("nodeBundledRoots() second call failed: %v", err)
	}
	if len(first) < 2 || len(second) != len(first) {
		t.Fatalf("nodeBundledRoots() lengths = %d and %d, want matching lengths of at least 2", len(first), len(second))
	}
	if first[0] == second[0] {
		t.Fatal("nodeBundledRoots()[0] pointers are shared, want defensive certificate copies")
	}
	wantRaw := bytes.Clone(second[0].Raw)
	wantCommonName := second[0].Subject.CommonName
	first[0].Raw[0] ^= 0xff
	first[0].Subject.CommonName = "mutated"
	first[1] = nil

	third, err := nodeBundledRoots()
	if err != nil {
		t.Fatalf("nodeBundledRoots() after caller mutation failed: %v", err)
	}
	if !bytes.Equal(second[0].Raw, wantRaw) || !bytes.Equal(third[0].Raw, wantRaw) {
		t.Error("nodeBundledRoots()[0].Raw changed after caller mutation, want independent DER storage")
	}
	if second[0].Subject.CommonName != wantCommonName || third[0].Subject.CommonName != wantCommonName {
		t.Errorf("nodeBundledRoots()[0].Subject.CommonName after mutation = %q and %q, want %q", second[0].Subject.CommonName, third[0].Subject.CommonName, wantCommonName)
	}
	if second[1] == nil || third[1] == nil {
		t.Error("nodeBundledRoots()[1] became nil after caller slice mutation, want independent slices")
	}
}

func TestNodeSpecificTrustRuntimeOptionsFailClosed(t *testing.T) {
	for _, environment := range []collectors.Environment{
		{"NODE_EXTRA_CA_CERTS": "/tmp/extra.pem"},
		{"NODE_USE_SYSTEM_CA": "1"},
		{"NODE_OPTIONS": "--trace-warnings --use-system-ca"},
		{"NODE_OPTIONS": "--use-openssl-ca"},
	} {
		_, err := trustedCertificates(environment, false)
		failure := requireProcessFailure(t, err, "REST_CA_RUNTIME_OPTIONS_UNSUPPORTED")
		if failure.Message != "Node-specific CA runtime options are not supported by the Go transport" {
			t.Errorf("runtime-option failure = %q", failure.Message)
		}
	}
	for _, environment := range []collectors.Environment{
		{},
		{"NODE_EXTRA_CA_CERTS": ""},
		{"NODE_USE_SYSTEM_CA": "0"},
		{"NODE_OPTIONS": "--trace-warnings --use-bundled-ca"},
	} {
		if _, err := trustedCertificates(environment, false); err != nil {
			t.Errorf("trustedCertificates(%v) failed: %v", environment, err)
		}
	}
}

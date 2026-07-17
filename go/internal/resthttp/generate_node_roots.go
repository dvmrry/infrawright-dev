//go:build ignore

// generate_node_roots captures and verifies the exact bundled trust-anchor
// set exposed by the frozen Node 24.15.0 oracle.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

const (
	outputPath          = "node_roots_data.go"
	noticePath          = "NODE_ROOTS_NOTICE.txt"
	wantNodeVersion     = "v24.15.0"
	wantOpenSSLVersion  = "3.6.2"
	wantUnicodeVersion  = "16.0"
	wantRootCount       = 145
	wantTotalDERBytes   = 154758
	wantOrderedDigest   = "1ca141cc18a277855d509ea58ac4a2eeb3d079344e3496d50557e6a1609634d3"
	wantSetDigest       = "58dd2d2c5037f7d9f5b0b2d182113230381e81f624162360dd99eaaea25dfc7c"
	wantFirstRootDigest = "73c176434f1bc6d5adf45b0e76e727287c8de57616c1e6e6141a2b2cbc7d8e4c"
	wantLastRootDigest  = "b49141502d00663d740f2e7ec340c52800962666121a36d09cf7dd2b90384fb4"
	wantNoticeDigest    = "44847eba50a8091f0a7c351d0814d5503ff78d350dd8d977156d9e598b0d7f5f"
	wantOutputDigest    = "6c195a48aca240bf5a82ecf54b53f78b59228c58b6800f9ec1608384765a39e9"
)

const oracleScript = `
const { X509Certificate } = require('node:crypto');
const { getCACertificates } = require('node:tls');
const certificates = getCACertificates('bundled').map((pem) =>
  new X509Certificate(pem).raw.toString('base64'));
process.stdout.write(JSON.stringify({
  node: process.version,
  openssl: process.versions.openssl,
  unicode: process.versions.unicode,
  certificates,
}));
`

type oracleOutput struct {
	Node         string   `json:"node"`
	OpenSSL      string   `json:"openssl"`
	Unicode      string   `json:"unicode"`
	Certificates []string `json:"certificates"`
}

func fail(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}

func digestDERs(ders [][]byte) (string, string) {
	ordered := sha256.New()
	individual := make([][]byte, 0, len(ders))
	var length [4]byte
	for _, der := range ders {
		binary.BigEndian.PutUint32(length[:], uint32(len(der)))
		ordered.Write(length[:])
		ordered.Write(der)
		digest := sha256.Sum256(der)
		individual = append(individual, append([]byte(nil), digest[:]...))
	}
	sort.Slice(individual, func(left, right int) bool {
		return bytes.Compare(individual[left], individual[right]) < 0
	})
	set := sha256.New()
	for _, digest := range individual {
		set.Write(digest)
	}
	return hex.EncodeToString(ordered.Sum(nil)), hex.EncodeToString(set.Sum(nil))
}

func cleanEnvironment() []string {
	blocked := map[string]struct{}{
		"NODE_EXTRA_CA_CERTS": {},
		"NODE_OPTIONS":        {},
		"NODE_USE_SYSTEM_CA":  {},
		"SSL_CERT_DIR":        {},
		"SSL_CERT_FILE":       {},
	}
	output := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, remove := blocked[name]; !remove {
			output = append(output, entry)
		}
	}
	return output
}

func main() {
	check := flag.Bool("check", false, "check that the generated Node root data is current without writing it")
	live := flag.Bool("live", false, "with --check, rederive the generated data from the frozen Node oracle")
	flag.Parse()
	if flag.NArg() != 0 || *live && !*check {
		fail("usage: go run generate_node_roots.go [--check [--live]]")
	}

	notice, err := os.ReadFile(noticePath)
	if err != nil {
		fail("read %s: %v", noticePath, err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(notice)); got != wantNoticeDigest {
		fail("%s SHA-256 = %s, want %s", noticePath, got, wantNoticeDigest)
	}
	if *check && !*live {
		current, readErr := os.ReadFile(outputPath)
		if readErr != nil {
			fail("read %s: %v", outputPath, readErr)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(current)); got != wantOutputDigest {
			fail("%s SHA-256 = %s, want %s; run go generate .", outputPath, got, wantOutputDigest)
		}
		return
	}

	command := exec.Command("node", "-e", oracleScript)
	command.Env = cleanEnvironment()
	raw, err := command.Output()
	if err != nil {
		fail("run frozen Node oracle: %v", err)
	}
	var oracle oracleOutput
	if err := json.Unmarshal(raw, &oracle); err != nil {
		fail("decode Node oracle output: %v", err)
	}
	if oracle.Node != wantNodeVersion || oracle.OpenSSL != wantOpenSSLVersion || oracle.Unicode != wantUnicodeVersion {
		fail("Node oracle identity = %s/OpenSSL %s/Unicode %s, want %s/%s/%s",
			oracle.Node, oracle.OpenSSL, oracle.Unicode,
			wantNodeVersion, wantOpenSSLVersion, wantUnicodeVersion)
	}
	if len(oracle.Certificates) != wantRootCount {
		fail("Node bundled-root count = %d, want %d", len(oracle.Certificates), wantRootCount)
	}
	ders := make([][]byte, 0, len(oracle.Certificates))
	total := 0
	for index, encoded := range oracle.Certificates {
		der, decodeErr := base64.StdEncoding.DecodeString(encoded)
		if decodeErr != nil {
			fail("decode root %d: %v", index, decodeErr)
		}
		ders = append(ders, der)
		total += len(der)
	}
	if total != wantTotalDERBytes {
		fail("Node bundled-root DER bytes = %d, want %d", total, wantTotalDERBytes)
	}
	orderedDigest, setDigest := digestDERs(ders)
	if orderedDigest != wantOrderedDigest || setDigest != wantSetDigest {
		fail("Node bundled-root digests = ordered %s/set %s, want %s/%s",
			orderedDigest, setDigest, wantOrderedDigest, wantSetDigest)
	}
	first := sha256.Sum256(ders[0])
	last := sha256.Sum256(ders[len(ders)-1])
	if fmt.Sprintf("%x", first) != wantFirstRootDigest || fmt.Sprintf("%x", last) != wantLastRootDigest {
		fail("Node bundled-root endpoints changed")
	}

	var framed bytes.Buffer
	var length [4]byte
	for _, der := range ders {
		binary.BigEndian.PutUint32(length[:], uint32(len(der)))
		framed.Write(length[:])
		framed.Write(der)
	}
	encoded := base64.StdEncoding.EncodeToString(framed.Bytes())
	var output bytes.Buffer
	output.WriteString(`// Code generated by go generate; DO NOT EDIT.
//
// Oracle: Node.js v24.15.0 tls.getCACertificates("bundled")
// OpenSSL: 3.6.2; Unicode: 16.0
// Upstream src/node_root_certs.h SHA-256: b8381bf64c65982dc9e93e87489e98f25cc660cfe46bd0f53ffaf99154ac6aac
// Upstream LICENSE SHA-256: 4573185d56580da2b890ba34a85a409257640f1c5632eade4300137266194d18
// Notice and provenance: NODE_ROOTS_NOTICE.txt.

package resthttp

const (
	nodeBundledRootCount = 145
	nodeBundledRootDERBytes = 154758
	nodeBundledRootOrderedDigest = "1ca141cc18a277855d509ea58ac4a2eeb3d079344e3496d50557e6a1609634d3"
	nodeBundledRootSetDigest = "58dd2d2c5037f7d9f5b0b2d182113230381e81f624162360dd99eaaea25dfc7c"
)

const nodeBundledRootsBase64 = "" +
`)
	for start := 0; start < len(encoded); start += 120 {
		end := min(start+120, len(encoded))
		fmt.Fprintf(&output, "\t%s +\n", strconv.Quote(encoded[start:end]))
	}
	output.WriteString("\t\"\"\n")
	formatted, err := format.Source(output.Bytes())
	if err != nil {
		fail("format generated Go: %v", err)
	}
	if *check {
		current, readErr := os.ReadFile(outputPath)
		if readErr != nil {
			fail("read %s: %v", outputPath, readErr)
		}
		if !bytes.Equal(current, formatted) {
			fail("%s is stale; run go generate .", outputPath)
		}
		return
	}
	if err := os.WriteFile(outputPath, formatted, 0o644); err != nil {
		fail("write %s: %v", outputPath, err)
	}
}

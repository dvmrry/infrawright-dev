package resthttp

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
)

var (
	nodeRootsOnce         sync.Once
	nodeRootsCertificates []*x509.Certificate
	nodeRootsError        error
)

func nodeRootDigests(ders [][]byte) (string, string) {
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

func loadNodeBundledRoots() {
	framed, err := base64.StdEncoding.DecodeString(nodeBundledRootsBase64)
	if err != nil {
		nodeRootsError = err
		return
	}
	ders := make([][]byte, 0, nodeBundledRootCount)
	for offset := 0; offset < len(framed); {
		if len(framed)-offset < 4 {
			nodeRootsError = errors.New("truncated Node bundled-root length")
			return
		}
		length := int(binary.BigEndian.Uint32(framed[offset : offset+4]))
		offset += 4
		if length <= 0 || length > len(framed)-offset {
			nodeRootsError = errors.New("invalid Node bundled-root length")
			return
		}
		ders = append(ders, framed[offset:offset+length])
		offset += length
	}
	if len(ders) != nodeBundledRootCount {
		nodeRootsError = errors.New("unexpected Node bundled-root count")
		return
	}
	total := 0
	for _, der := range ders {
		total += len(der)
	}
	if total != nodeBundledRootDERBytes {
		nodeRootsError = errors.New("unexpected Node bundled-root byte count")
		return
	}
	orderedDigest, setDigest := nodeRootDigests(ders)
	if orderedDigest != nodeBundledRootOrderedDigest || setDigest != nodeBundledRootSetDigest {
		nodeRootsError = errors.New("unexpected Node bundled-root digest")
		return
	}
	certificates := make([]*x509.Certificate, 0, len(ders))
	for _, der := range ders {
		certificate, parseErr := x509.ParseCertificate(der)
		if parseErr != nil {
			nodeRootsError = parseErr
			return
		}
		certificates = append(certificates, certificate)
	}
	nodeRootsCertificates = certificates
}

func nodeBundledRoots() ([]*x509.Certificate, error) {
	nodeRootsOnce.Do(loadNodeBundledRoots)
	if nodeRootsError != nil {
		return nil, nodeRootsError
	}
	certificates := make([]*x509.Certificate, len(nodeRootsCertificates))
	for index, certificate := range nodeRootsCertificates {
		cloned, err := x509.ParseCertificate(bytes.Clone(certificate.Raw))
		if err != nil {
			return nil, err
		}
		certificates[index] = cloned
	}
	return certificates, nil
}

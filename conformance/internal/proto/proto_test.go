package proto_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/saphan-ai/tests-studio/conformance/internal/proto"
)

// digestVectors are the known answers of specification section 7. They were computed outside
// this module (Python hashlib over the sorted UTF-8 ids, each followed by one LF byte), so the
// test does not check the implementation against itself.
var digestVectors = []struct {
	name string
	ids  []string
	want string
}{
	{"the empty set digests the empty input", nil, "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
	{"one id", []string{"evt-0001"}, "sha256:d5c51f60239f4d48591a58b5a6aad406fef54315c27eea24f38d800b922d38e4"},
	{"three ids given unsorted", []string{"evt-0003", "evt-0001", "evt-0002"}, "sha256:80e903cace39e18ffd3b42682fa4a5ac5cc3399b3570ddde071daefc295bb7c5"},
	{"ids that differ in case and punctuation sort by byte", []string{"evt-b", "evt-B", "evt-a.1", "evt-a-1"}, "sha256:d6b0186f9a60e0bd8323617e33d6494898e1ac37cb7150defd589a1b727351f1"},
}

func TestEventsDigestKnownAnswers(t *testing.T) {
	t.Parallel()
	for _, v := range digestVectors {
		t.Run(v.name, func(t *testing.T) {
			if got := proto.EventsDigest(v.ids); got != v.want {
				t.Fatalf("EventsDigest(%q) = %s; want %s", v.ids, got, v.want)
			}
		})
	}
}

// TestEventsDigestLayoutIsNotAnyOtherLayout is the negative control of the vectors: each other
// reading of "SHA-256 over the sorted ids" gives a different answer, so a core that picked one of
// them would fail the known answers above.
func TestEventsDigestLayoutIsNotAnyOtherLayout(t *testing.T) {
	t.Parallel()
	ids := []string{"evt-0003", "evt-0001", "evt-0002"}
	want := proto.EventsDigest(ids)
	hash := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return "sha256:" + hex.EncodeToString(sum[:])
	}
	others := map[string]string{
		"sorted, no terminator":              hash("evt-0001evt-0002evt-0003"),
		"sorted, joined by LF, no final LF":  hash(strings.Join([]string{"evt-0001", "evt-0002", "evt-0003"}, "\n")),
		"sorted, each followed by CRLF":      hash("evt-0001\r\nevt-0002\r\nevt-0003\r\n"),
		"unsorted, each followed by LF":      hash("evt-0003\nevt-0001\nevt-0002\n"),
		"sorted descending, each LF-ending":  hash("evt-0003\nevt-0002\nevt-0001\n"),
		"sorted, one id counted twice (bag)": hash("evt-0001\nevt-0001\nevt-0002\nevt-0003\n"),
	}
	for name, got := range others {
		if got == want {
			t.Errorf("layout %q gives the same digest as the specified one, so the vectors cannot tell them apart", name)
		}
	}
}

func TestEventsDigestDoesNotReorderItsInput(t *testing.T) {
	t.Parallel()
	ids := []string{"evt-0003", "evt-0001"}
	proto.EventsDigest(ids)
	if ids[0] != "evt-0003" || ids[1] != "evt-0001" {
		t.Fatalf("EventsDigest sorted the caller's slice in place: %q", ids)
	}
}

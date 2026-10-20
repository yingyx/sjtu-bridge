package cli

import (
	"reflect"
	"testing"
)

func TestReuseLoginTargetsRequiresActiveCampusSource(t *testing.T) {
	for _, test := range []struct {
		name, raw, source, code string
	}{
		{"invalid_metadata", `{`, "source", "UPSTREAM_CHANGED"},
		{"missing", `{"credentials":{}}`, "source", "AUTH_REQUIRED"},
		{"inactive", `{"credentials":{"source":{"provider":"teaching","kind":"JAAuthCookie","active":false}}}`, "source", "AUTH_REQUIRED"},
		{"canvas", `{"credentials":{"source":{"provider":"canvas","kind":"CanvasToken","active":true}}}`, "source", "AUTH_REQUIRED"},
		{"wrong_kind", `{"credentials":{"source":{"provider":"teaching","kind":"CanvasToken","active":true}}}`, "source", "AUTH_REQUIRED"},
		{"unsupported_portal", `{"credentials":{"source":{"provider":"jaccount","kind":"JAAuthCookie","active":true}}}`, "source", "AUTH_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			targets, err := reuseLoginTargets([]byte(test.raw), test.source, []string{"teaching", "sjtumail"}, false)
			if err == nil || loginErrorCode(err) != test.code || targets != nil {
				t.Fatal("invalid source was accepted")
			}
		})
	}
}

func TestReuseLoginTargetsSelectsOnlyOtherPortal(t *testing.T) {
	for _, test := range []struct {
		provider, target string
	}{
		{"teaching", "sjtumail"}, {"sjtumail", "teaching"},
	} {
		raw := []byte(`{"credentials":{"source":{"provider":"` + test.provider + `","kind":"JAAuthCookie","active":true}}}`)
		for _, explicit := range []bool{false, true} {
			providers := []string{"teaching", "sjtumail"}
			if explicit {
				providers = []string{test.target}
			}
			got, err := reuseLoginTargets(raw, "source", providers, explicit)
			if err != nil || !reflect.DeepEqual(got, []string{test.target}) {
				t.Fatal("other portal was not selected")
			}
		}
		for _, providers := range [][]string{{test.provider}, {test.provider, test.target}} {
			if _, err := reuseLoginTargets(raw, "source", providers, true); err == nil || loginErrorCode(err) != "INVALID_ARGUMENT" {
				t.Fatal("explicit source portal was accepted as target")
			}
		}
	}
}

func TestReuseLoginTargetsExplicitActivityPortal(t *testing.T) {
	raw := []byte(`{"credentials":{"source":{"provider":"teaching","kind":"JAAuthCookie","active":true}}}`)
	got, e := reuseLoginTargets(raw, "source", []string{"activity"}, true)
	if e != nil || !reflect.DeepEqual(got, []string{"activity"}) {
		t.Fatal("explicit activity target unavailable", e)
	}
	raw = []byte(`{"credentials":{"source":{"provider":"activity","kind":"JAAuthCookie","active":true}}}`)
	if _, e = reuseLoginTargets(raw, "source", []string{"activity"}, true); e == nil {
		t.Fatal("activity source selected as its own target")
	}
}

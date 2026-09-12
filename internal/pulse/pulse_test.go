package pulse

import "testing"

func TestSubject(t *testing.T) {
	got := Subject("geo", "abc.def")
	want := "WAF_STATUS.service.geo.abc_def"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

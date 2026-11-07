package install

import "testing"

func TestAtmDataPlaneBase(t *testing.T) {
	cases := map[string]string{
		"":                         "http://127.0.0.1:8081",
		"http://10.0.0.5:8081":     "http://10.0.0.5:8081",
		"http://10.0.0.5:8080":     "http://10.0.0.5:8081",
		"http://10.0.0.5":          "http://10.0.0.5:8081",
		"https://portal.local:8443": "https://portal.local:8443",
	}
	for in, want := range cases {
		if got := atmDataPlaneBase(in); got != want {
			t.Fatalf("atmDataPlaneBase(%q)=%q want %q", in, got, want)
		}
	}
}

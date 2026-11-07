package store

import "testing"

func TestBindPlaceholders(t *testing.T) {
	s := &Store{driver: "postgres"}
	got := s.bind("SELECT * FROM t WHERE a=? AND b=?")
	if got != "SELECT * FROM t WHERE a=$1 AND b=$2" {
		t.Fatalf("postgres bind: %s", got)
	}
	s.driver = "sqlite"
	if s.bind("WHERE a=?") != "WHERE a=?" {
		t.Fatal("sqlite bind should keep '?'")
	}
	if normalizeDriver("pgx") != "postgres" || normalizeDriver("") != "sqlite" {
		t.Fatal("driver alias")
	}
}

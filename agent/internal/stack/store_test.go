package stack

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "stackman.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSaveGetListRemove(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	st := &Stack{
		Name:        "hotel",
		Path:        "/srv/hotel",
		Remote:      "git@example.com:hotel.git",
		Branch:      "main",
		ComposeFile: "docker-compose.yml",
		Allow:       []string{"next", "ws"},
		Exclude:     []string{"mysql"},
	}
	if err := s.Save(ctx, st); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if st.ID == "" {
		t.Fatal("expected generated ID")
	}

	got, err := s.Get(ctx, "hotel")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Path != st.Path || got.Remote != st.Remote || got.Branch != st.Branch {
		t.Fatalf("got %+v", got)
	}
	if len(got.Allow) != 2 || got.Allow[0] != "next" || got.Allow[1] != "ws" {
		t.Fatalf("allow = %v", got.Allow)
	}
	if len(got.Exclude) != 1 || got.Exclude[0] != "mysql" {
		t.Fatalf("exclude = %v", got.Exclude)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatal("timestamps not set")
	}

	list, err := s.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v, %v", list, err)
	}
	if n, _ := s.Count(ctx); n != 1 {
		t.Fatalf("Count = %d", n)
	}

	if err := s.Remove(ctx, "hotel"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := s.Get(ctx, "hotel"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after remove = %v", err)
	}
	if n, _ := s.Count(ctx); n != 0 {
		t.Fatalf("Count after remove = %d", n)
	}
}

func TestSaveIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	st := &Stack{Name: "hotel", Path: "/srv/hotel", Allow: []string{"a"}}
	if err := s.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	firstID := st.ID
	firstCreated := st.CreatedAt

	st.Allow = []string{"b"}
	st.Branch = "next"
	if err := s.Save(ctx, st); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	if st.ID != firstID {
		t.Fatalf("id changed: %s -> %s", firstID, st.ID)
	}
	if !st.CreatedAt.Equal(firstCreated) {
		t.Fatalf("created_at changed")
	}
	got, err := s.Get(ctx, "hotel")
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "next" || len(got.Allow) != 1 || got.Allow[0] != "b" {
		t.Fatalf("got %+v", got)
	}
}

func TestSaveConflicts(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if err := s.Save(ctx, &Stack{Name: "a", Path: "/srv/a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, &Stack{Name: "a", Path: "/srv/other"}); !errors.Is(err, ErrExists) {
		t.Fatalf("name conflict = %v", err)
	}
	if err := s.Save(ctx, &Stack{Name: "b", Path: "/srv/a"}); !errors.Is(err, ErrExists) {
		t.Fatalf("path conflict = %v", err)
	}
}

func TestSetPolicy(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	st := &Stack{Name: "hotel", Path: "/srv/hotel"}
	if err := s.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPolicy(ctx, st.ID, "mysql", PolicyExclude); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPolicy(ctx, st.ID, "next", PolicyAllow); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "hotel")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Exclude) != 1 || len(got.Allow) != 1 {
		t.Fatalf("policies = %+v", got)
	}
	if err := s.SetPolicy(ctx, st.ID, "next", ""); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, "hotel")
	if len(got.Allow) != 0 {
		t.Fatalf("allow not cleared: %v", got.Allow)
	}
	if err := s.SetPolicy(ctx, st.ID, "next", "bogus"); err == nil {
		t.Fatal("expected invalid policy error")
	}
}

func TestStorePersistsAcrossOpen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "stackman.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, &Stack{Name: "hotel", Path: "/srv/hotel"}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	if _, err := s.Get(ctx, "hotel"); err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
}

func TestEffectiveManaged(t *testing.T) {
	tests := []struct {
		name     string
		all      []string
		allow    []string
		exclude  []string
		expected []string
	}{
		{"all when no allow", []string{"a", "b", "c"}, nil, nil, []string{"a", "b", "c"}},
		{"allow limits", []string{"a", "b", "c"}, []string{"a", "c"}, nil, []string{"a", "c"}},
		{"exclude wins", []string{"a", "b", "c"}, nil, []string{"b"}, []string{"a", "c"}},
		{"allow minus exclude", []string{"a", "b", "c"}, []string{"a", "b"}, []string{"b"}, []string{"a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EffectiveManaged(tt.all, tt.allow, tt.exclude)
			if len(got) != len(tt.expected) {
				t.Fatalf("got %v want %v", got, tt.expected)
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Fatalf("got %v want %v", got, tt.expected)
				}
			}
		})
	}
}

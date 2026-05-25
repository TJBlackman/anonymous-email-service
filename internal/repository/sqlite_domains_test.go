package repository

import (
	"context"
	"errors"
	"testing"
)

func TestSQLiteCreateDomain(t *testing.T) {
	repo := openTestSQLite(t)
	ctx := context.Background()

	domain, err := repo.CreateDomain(ctx, "Example.Test")
	if err != nil {
		t.Fatalf("CreateDomain() error = %v", err)
	}
	if domain.ID == 0 {
		t.Fatal("CreateDomain() did not assign ID")
	}
	if domain.Name != "example.test" {
		t.Fatalf("Name = %q, want lowercased %q", domain.Name, "example.test")
	}
	if !domain.Enabled {
		t.Fatal("new domain should be enabled")
	}
	if domain.CreatedAt.IsZero() {
		t.Fatal("CreateDomain() did not assign CreatedAt")
	}
}

func TestSQLiteCreateDomainConflictIsCaseInsensitive(t *testing.T) {
	repo := openTestSQLite(t)
	ctx := context.Background()

	if _, err := repo.CreateDomain(ctx, "example.test"); err != nil {
		t.Fatalf("CreateDomain() error = %v", err)
	}

	_, err := repo.CreateDomain(ctx, "EXAMPLE.TEST")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateDomain() duplicate error = %v, want ErrConflict", err)
	}
}

func TestSQLiteListDomainsOrderingAndFilter(t *testing.T) {
	repo := openTestSQLite(t)
	ctx := context.Background()

	for _, name := range []string{"first.test", "second.test", "third.test"} {
		if _, err := repo.CreateDomain(ctx, name); err != nil {
			t.Fatalf("CreateDomain(%q) error = %v", name, err)
		}
	}
	if err := repo.SetDomainEnabled(ctx, "second.test", false); err != nil {
		t.Fatalf("SetDomainEnabled() error = %v", err)
	}

	all, err := repo.ListDomains(ctx, false)
	if err != nil {
		t.Fatalf("ListDomains(false) error = %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListDomains(false) len = %d, want 3", len(all))
	}
	if all[0].Name != "first.test" || all[2].Name != "third.test" {
		t.Fatalf("ListDomains ordering = %q, %q, %q; want oldest first", all[0].Name, all[1].Name, all[2].Name)
	}

	enabled, err := repo.ListDomains(ctx, true)
	if err != nil {
		t.Fatalf("ListDomains(true) error = %v", err)
	}
	if len(enabled) != 2 {
		t.Fatalf("ListDomains(true) len = %d, want 2 (second disabled)", len(enabled))
	}
}

func TestSQLiteGetDomain(t *testing.T) {
	repo := openTestSQLite(t)
	ctx := context.Background()

	if _, err := repo.CreateDomain(ctx, "example.test"); err != nil {
		t.Fatalf("CreateDomain() error = %v", err)
	}

	got, err := repo.GetDomain(ctx, "EXAMPLE.TEST")
	if err != nil {
		t.Fatalf("GetDomain() error = %v", err)
	}
	if got.Name != "example.test" {
		t.Fatalf("GetDomain().Name = %q", got.Name)
	}

	_, err = repo.GetDomain(ctx, "missing.test")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetDomain(missing) error = %v, want ErrNotFound", err)
	}
}

func TestSQLiteSetDomainEnabled(t *testing.T) {
	repo := openTestSQLite(t)
	ctx := context.Background()

	if _, err := repo.CreateDomain(ctx, "example.test"); err != nil {
		t.Fatalf("CreateDomain() error = %v", err)
	}

	if err := repo.SetDomainEnabled(ctx, "example.test", false); err != nil {
		t.Fatalf("SetDomainEnabled(false) error = %v", err)
	}
	got, err := repo.GetDomain(ctx, "example.test")
	if err != nil {
		t.Fatalf("GetDomain() error = %v", err)
	}
	if got.Enabled {
		t.Fatal("domain should be disabled")
	}

	if err := repo.SetDomainEnabled(ctx, "missing.test", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetDomainEnabled(missing) error = %v, want ErrNotFound", err)
	}
}

func TestSQLiteIsDomainEnabled(t *testing.T) {
	repo := openTestSQLite(t)
	ctx := context.Background()

	if _, err := repo.CreateDomain(ctx, "example.test"); err != nil {
		t.Fatalf("CreateDomain() error = %v", err)
	}

	enabled, err := repo.IsDomainEnabled(ctx, "EXAMPLE.TEST")
	if err != nil {
		t.Fatalf("IsDomainEnabled() error = %v", err)
	}
	if !enabled {
		t.Fatal("IsDomainEnabled() = false, want true")
	}

	if err := repo.SetDomainEnabled(ctx, "example.test", false); err != nil {
		t.Fatalf("SetDomainEnabled() error = %v", err)
	}
	enabled, err = repo.IsDomainEnabled(ctx, "example.test")
	if err != nil {
		t.Fatalf("IsDomainEnabled() error = %v", err)
	}
	if enabled {
		t.Fatal("disabled domain reported enabled")
	}

	enabled, err = repo.IsDomainEnabled(ctx, "missing.test")
	if err != nil {
		t.Fatalf("IsDomainEnabled(missing) error = %v, want nil", err)
	}
	if enabled {
		t.Fatal("missing domain reported enabled")
	}
}

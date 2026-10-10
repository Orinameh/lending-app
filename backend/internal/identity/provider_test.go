package identity

import (
	"context"
	"testing"
)

func TestFakeBVNMatch(t *testing.T) {
	p := FakeProvider{}
	res, err := p.VerifyBVN(context.Background(), "12345678901", "Ada", "Obi")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Match || res.Ref == "" {
		t.Fatalf("expected match with ref, got %+v", res)
	}
}

func TestFakeBVNMismatch(t *testing.T) {
	p := FakeProvider{}
	res, err := p.VerifyBVN(context.Background(), "12345678900", "Ada", "Obi")
	if err != nil {
		t.Fatal(err)
	}
	if res.Match || res.Reason == "" {
		t.Fatalf("expected mismatch with reason, got %+v", res)
	}
}

func TestFakeBVNOutage(t *testing.T) {
	p := FakeProvider{}
	if _, err := p.VerifyBVN(context.Background(), "12345678999", "Ada", "Obi"); err == nil {
		t.Fatal("expected simulated outage error")
	}
}

func TestFakeNINPaths(t *testing.T) {
	p := FakeProvider{}
	if res, _ := p.VerifyNIN(context.Background(), "12345678901", "Ada", "Obi"); !res.Match {
		t.Fatal("expected NIN match")
	}
	if res, _ := p.VerifyNIN(context.Background(), "12345999901", "Ada", "Obi"); res.Match {
		t.Fatal("expected NIN mismatch on 999")
	}
	if _, err := p.VerifyNIN(context.Background(), "12345678988", "Ada", "Obi"); err == nil {
		t.Fatal("expected simulated outage error")
	}
}

func TestFakeRequiresNames(t *testing.T) {
	p := FakeProvider{}
	if res, _ := p.VerifyBVN(context.Background(), "12345678901", "", "Obi"); res.Match {
		t.Fatal("empty names must not match")
	}
}

func TestFakeRejectsShort(t *testing.T) {
	p := FakeProvider{}
	if _, err := p.VerifyBVN(context.Background(), "12", "A", "B"); err == nil {
		t.Fatal("short BVN must error, not panic")
	}
}

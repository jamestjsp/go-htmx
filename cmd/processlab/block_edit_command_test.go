package main

import "testing"

func TestBlockCountText(t *testing.T) {
	if got := blockCountText(1); got != "1 block" {
		t.Fatalf("single block text = %q, want %q", got, "1 block")
	}
	if got := blockCountText(3); got != "3 blocks" {
		t.Fatalf("multiple block text = %q, want %q", got, "3 blocks")
	}
}

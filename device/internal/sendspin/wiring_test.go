package sendspin

import (
	"testing"

	"github.com/wilbowes/EchoMuse/internal/musicplane"
)

// The two seams this package is wired through, checked at compile time here
// rather than discovered in cmd/server.go — which is behind a cgo build tag
// and cannot be built on the host, so a mismatch there is found by CI at
// best and by a device at worst.

func TestTheArbitersViewIsAPlaneOwner(t *testing.T) {
	// musicplane.Scoped is what the client is handed. It satisfies
	// PlaneOwner structurally, which means nothing enforces it: adding a
	// parameter to Claim on either side compiles on both sides and fails
	// only where they meet.
	var _ PlaneOwner = musicplane.Scoped{}

	// And it behaves, not just type-checks: a claim through the view has to
	// reach the arbiter, or the runtime writes audio nobody arbitrated.
	var o musicplane.Owner
	o.Register(musicplane.Sendspin, nil)
	view := o.For(musicplane.Sendspin)

	var plane PlaneOwner = view
	if !plane.Claim() {
		t.Fatal("the claim was refused")
	}
	if o.Owner() != musicplane.Sendspin {
		t.Fatalf("the arbiter says %v owns the plane", o.Owner())
	}
	if !plane.MayWrite() {
		t.Fatal("the owner may not write")
	}
	plane.Release()
	if o.Owner() != musicplane.None {
		t.Fatalf("the plane was not released: %v", o.Owner())
	}
}

func TestTheRuntimeIsAHandler(t *testing.T) {
	// Handler has eight methods and a rename on either side produces a Conn
	// that compiles and calls nothing.
	var _ Handler = (*Runtime)(nil)
	var _ Handler = NewRuntime(nil, nil, nil, 0)
}

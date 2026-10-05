package auth

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestMain hashes at bcrypt's cheapest cost: under the race detector a single
// hash at the production cost takes seconds. A test that measures the work
// factor sets its own with SetBcryptCostForTesting.
func TestMain(m *testing.M) {
	SetBcryptCostForTesting(bcrypt.MinCost)
	os.Exit(m.Run())
}

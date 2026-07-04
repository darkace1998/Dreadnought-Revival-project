package dreadgameconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWeaponProjectileIntegration(t *testing.T) {
	// Set up DATA_DIR environment variable
	original := os.Getenv("DATA_DIR")
	defer func() { _ = os.Setenv("DATA_DIR", original) }()

	dataDir := filepath.Join("..", "..", "data")
	if err := os.Setenv("DATA_DIR", dataDir); err != nil {
		t.Fatal(err)
	}

	// Load weapons and projectiles
	weaponsLoaded = false
	weaponsByItemID = nil
	projectilesLoaded = false
	projectilesByRowName = nil

	if err := LoadWeapons(); err != nil {
		t.Fatalf("LoadWeapons() error = %v", err)
	}

	if err := LoadProjectiles(); err != nil {
		t.Fatalf("LoadProjectiles() error = %v", err)
	}

	// Test some known weapon-projectile mappings
	testCases := []struct {
		weaponItemID    int32
		expectedProjectile string
	} {
		{100597762, "WP_AssaultHPri01_proj01_BP"}, // WP_AssaultHPri01_weapon01_BP
		{100597763, "WP_AssaultHPri01_proj01_T3_BP"}, // WP_AssaultHPri01_weapon01_T3_BP
	}

	for _, tc := range testCases {
		weapon, ok := WeaponByID(tc.weaponItemID)
		if !ok {
			t.Errorf("WeaponByID(%d) not found", tc.weaponItemID)
			continue
		}

		projectile, ok := ProjectileForWeapon(weapon)
		if !ok {
			t.Errorf("ProjectileForWeapon(%d) not found", tc.weaponItemID)
			continue
		}

		if projectile.RowName != tc.expectedProjectile {
			t.Errorf("ProjectileForWeapon(%d).RowName = %q, want %q", tc.weaponItemID, projectile.RowName, tc.expectedProjectile)
		}
	}
}

func TestDeriveProjectileRowName(t *testing.T) {
	testCases := []struct {
		weaponName     string
		expectedProjectile string
	} {
		{"WP_AssaultHPri01_weapon01_BP", "WP_AssaultHPri01_proj01_BP"},
		{"WP_AssaultHPri01_weapon01_T3_BP", "WP_AssaultHPri01_proj01_T3_BP"}, // Tier suffix preserved
		{"WP_AssaultMPri01_weapon01_BP", "WP_AssaultMPri01_proj01_BP"},
		{"NonProjectileWeapon_BP", ""},
	}

	for _, tc := range testCases {
		result := deriveProjectileRowName(tc.weaponName)
		if result != tc.expectedProjectile {
			t.Errorf("deriveProjectileRowName(%q) = %q, want %q", tc.weaponName, result, tc.expectedProjectile)
		}
	}
}
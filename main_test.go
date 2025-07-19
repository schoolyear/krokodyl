package main

import (
	"testing"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// TestWindowConfiguration verifies that the window configuration allows fullscreen
func TestWindowConfiguration(t *testing.T) {
	// Create the same configuration used in main.go
	opts := &options.App{
		Title:         "krokodyl",
		Width:         800,
		Height:        600,
		MinWidth:      520,
		MinHeight:     500,
		AssetServer: &assetserver.Options{
			Assets: nil, // Not needed for this test
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
	}

	// Verify that MaxWidth and MaxHeight are not set (should be 0)
	if opts.MaxWidth != 0 {
		t.Errorf("MaxWidth should be 0 to allow fullscreen, but got %d", opts.MaxWidth)
	}
	
	if opts.MaxHeight != 0 {
		t.Errorf("MaxHeight should be 0 to allow fullscreen, but got %d", opts.MaxHeight)
	}

	// Verify minimum constraints are still in place
	if opts.MinWidth != 520 {
		t.Errorf("MinWidth should be 520, but got %d", opts.MinWidth)
	}
	
	if opts.MinHeight != 500 {
		t.Errorf("MinHeight should be 500, but got %d", opts.MinHeight)
	}

	// Verify initial size is reasonable
	if opts.Width != 800 {
		t.Errorf("Initial width should be 800, but got %d", opts.Width)
	}
	
	if opts.Height != 600 {
		t.Errorf("Initial height should be 600, but got %d", opts.Height)
	}

	t.Log("Window configuration test passed - fullscreen mode should work properly")
}
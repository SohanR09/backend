package geo

import "testing"

var square = []Point{
	{37.7749, -122.4194},
	{37.7849, -122.4194},
	{37.7849, -122.4094},
	{37.7749, -122.4094},
	{37.7749, -122.4194},
}

// An L-shaped (concave) polygon: the notch at the top right is outside.
var lShape = []Point{
	{0, 0}, {0, 10}, {5, 10}, {5, 5}, {10, 5}, {10, 0}, {0, 0},
}

func TestPointInPolygon(t *testing.T) {
	tests := []struct {
		name     string
		ring     []Point
		lat, lon float64
		want     bool
	}{
		{"center of square", square, 37.7799, -122.4144, true},
		{"outside north", square, 37.8, -122.4144, false},
		{"outside west", square, 37.7799, -122.5, false},
		{"on edge counts as inside", square, 37.7849, -122.4144, true},
		{"on vertex counts as inside", square, 37.7749, -122.4194, true},
		{"concave inside arm", lShape, 2, 2, true},
		{"concave inside lower arm", lShape, 8, 2, true},
		{"concave notch is outside", lShape, 8, 8, false},
		{"far away", lShape, 50, 50, false},
		{"degenerate ring", []Point{{0, 0}, {1, 1}}, 0.5, 0.5, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PointInPolygon(tt.lat, tt.lon, tt.ring); got != tt.want {
				t.Fatalf("PointInPolygon(%v,%v)=%v want %v", tt.lat, tt.lon, got, tt.want)
			}
		})
	}
}

func TestValidatePolygon(t *testing.T) {
	if err := ValidatePolygon(square); err != nil {
		t.Fatalf("valid polygon rejected: %v", err)
	}
	if err := ValidatePolygon(square[:3]); err == nil {
		t.Fatal("expected error for too few points")
	}
	open := append([]Point{}, square[:4]...)
	open = append(open, Point{1, 1})
	if err := ValidatePolygon(open); err == nil {
		t.Fatal("expected error for unclosed polygon")
	}
	bad := []Point{{95, 0}, {0, 0}, {0, 1}, {95, 0}}
	if err := ValidatePolygon(bad); err == nil {
		t.Fatal("expected error for latitude out of range")
	}
	badLon := []Point{{0, 190}, {0, 0}, {1, 1}, {0, 190}}
	if err := ValidatePolygon(badLon); err == nil {
		t.Fatal("expected error for longitude out of range")
	}
}

func TestBoundingBox(t *testing.T) {
	b := BoundingBox(square)
	if !b.Contains(37.78, -122.41) {
		t.Fatal("bbox should contain interior point")
	}
	if b.Contains(38, -122.41) {
		t.Fatal("bbox should not contain far point")
	}
}

func TestValidateCoordinate(t *testing.T) {
	if err := ValidateCoordinate(91, 0); err == nil {
		t.Fatal("expected latitude error")
	}
	if err := ValidateCoordinate(0, -181); err == nil {
		t.Fatal("expected longitude error")
	}
	if err := ValidateCoordinate(12.9, 77.6); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Package geo contains the geospatial primitives used by the backend:
// polygon validation, bounding boxes and point-in-polygon tests.
package geo

import (
	"errors"
	"fmt"
	"math"
)

// Point is a [latitude, longitude] pair.
type Point = [2]float64

// BBox is an axis-aligned bounding box used as a cheap pre-filter.
type BBox struct {
	MinLat, MaxLat, MinLon, MaxLon float64
}

// ValidateCoordinate checks that lat/lon are finite and within range.
func ValidateCoordinate(lat, lon float64) error {
	if math.IsNaN(lat) || math.IsInf(lat, 0) || math.IsNaN(lon) || math.IsInf(lon, 0) {
		return errors.New("coordinates must be finite numbers")
	}
	if lat < -90 || lat > 90 {
		return fmt.Errorf("latitude %v out of range [-90, 90]", lat)
	}
	if lon < -180 || lon > 180 {
		return fmt.Errorf("longitude %v out of range [-180, 180]", lon)
	}
	return nil
}

// ValidatePolygon enforces the API rules: at least 4 points, a closed ring
// and every vertex inside valid latitude/longitude ranges.
func ValidatePolygon(ring []Point) error {
	if len(ring) < 4 {
		return errors.New("polygon needs at least 4 points (3 unique + closing point)")
	}
	if ring[0] != ring[len(ring)-1] {
		return errors.New("polygon must be closed: first and last coordinates must be identical")
	}
	for i, p := range ring {
		if err := ValidateCoordinate(p[0], p[1]); err != nil {
			return fmt.Errorf("coordinates[%d]: %w", i, err)
		}
	}
	return nil
}

// BoundingBox returns the bounding box of the ring.
func BoundingBox(ring []Point) BBox {
	b := BBox{MinLat: math.Inf(1), MaxLat: math.Inf(-1), MinLon: math.Inf(1), MaxLon: math.Inf(-1)}
	for _, p := range ring {
		b.MinLat = math.Min(b.MinLat, p[0])
		b.MaxLat = math.Max(b.MaxLat, p[0])
		b.MinLon = math.Min(b.MinLon, p[1])
		b.MaxLon = math.Max(b.MaxLon, p[1])
	}
	return b
}

// Contains reports whether the point lies inside the box (edges included).
func (b BBox) Contains(lat, lon float64) bool {
	return lat >= b.MinLat && lat <= b.MaxLat && lon >= b.MinLon && lon <= b.MaxLon
}

const onSegmentEpsilon = 1e-12

// PointInPolygon reports whether (lat, lon) is inside the closed ring.
// Points lying exactly on an edge or vertex count as inside. It uses the
// even-odd ray casting rule with latitude as Y and longitude as X.
func PointInPolygon(lat, lon float64, ring []Point) bool {
	n := len(ring)
	if n < 4 {
		return false
	}
	inside := false
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		yi, xi := ring[i][0], ring[i][1]
		yj, xj := ring[j][0], ring[j][1]

		if onSegment(lat, lon, yi, xi, yj, xj) {
			return true
		}
		if (yi > lat) != (yj > lat) {
			xCross := (xj-xi)*(lat-yi)/(yj-yi) + xi
			if lon < xCross {
				inside = !inside
			}
		}
	}
	return inside
}

// onSegmentCollinear reports whether p is (numerically) collinear with a-b.
func onSegmentCollinear(py, px, ay, ax, by, bx float64) bool {
	cross := (px-ax)*(by-ay) - (py-ay)*(bx-ax)
	return math.Abs(cross) <= onSegmentEpsilon
}

// onSegment reports whether point p lies on the segment a-b.
func onSegment(py, px, ay, ax, by, bx float64) bool {
	if !onSegmentCollinear(py, px, ay, ax, by, bx) {
		return false
	}
	return px >= math.Min(ax, bx)-onSegmentEpsilon && px <= math.Max(ax, bx)+onSegmentEpsilon &&
		py >= math.Min(ay, by)-onSegmentEpsilon && py <= math.Max(ay, by)+onSegmentEpsilon
}

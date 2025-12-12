package main

import (
	"math"
	"strconv"
	"strings"
)

type Transform struct {
	A, B, C, D, E, F float64 // 2x3 matrix: [ A C E ; B D F ]
}

func identityTransform() Transform {
	return Transform{A: 1, D: 1} // [1 0 0; 0 1 0]
}

func (t Transform) Mul(u Transform) Transform {
	// t ∘ u (apply u, then t)
	return Transform{
		A: t.A*u.A + t.C*u.B,
		B: t.B*u.A + t.D*u.B,
		C: t.A*u.C + t.C*u.D,
		D: t.B*u.C + t.D*u.D,
		E: t.A*u.E + t.C*u.F + t.E,
		F: t.B*u.E + t.D*u.F + t.F,
	}
}

func (t Transform) Apply(p Point) Point {
	return Point{
		X: t.A*p.X + t.C*p.Y + t.E,
		Y: t.B*p.X + t.D*p.Y + t.F,
	}
}

func parseTransformAttr(s string) Transform {
	s = strings.TrimSpace(s)
	if s == "" {
		return identityTransform()
	}
	// handle only translate(x[,y]) for now
	if strings.HasPrefix(s, "translate") {
		open := strings.IndexByte(s, '(')
		close := strings.IndexByte(s, ')')
		if open >= 0 && close > open {
			args := strings.Split(s[open+1:close], ",")
			if len(args) == 1 {
				args = strings.Split(args[0], " ")
			}
			var tx, ty float64
			if len(args) >= 1 {
				tx, _ = strconv.ParseFloat(strings.TrimSpace(args[0]), 64)
			}
			if len(args) >= 2 {
				ty, _ = strconv.ParseFloat(strings.TrimSpace(args[1]), 64)
			}
			return Transform{A: 1, D: 1, E: tx, F: ty}
		}
	}
	// unsupported transform -> identity for now
	return identityTransform()
}

type Point struct {
	X, Y float64
}

func lerp(a, b Point, t float64) Point {
	return Point{
		X: a.X + (b.X-a.X)*t,
		Y: a.Y + (b.Y-a.Y)*t,
	}
}

func distPointToLine(p, a, b Point) float64 {
	// distance from p to line segment a-b (we treat as infinite line for flatness)
	dx := b.X - a.X
	dy := b.Y - a.Y
	if dx == 0 && dy == 0 {
		// a and b are the same
		return math.Hypot(p.X-a.X, p.Y-a.Y)
	}
	// project p onto line ab, compute perpendicular distance
	t := ((p.X-a.X)*dx + (p.Y-a.Y)*dy) / (dx*dx + dy*dy)
	px := a.X + t*dx
	py := a.Y + t*dy
	return math.Hypot(p.X-px, p.Y-py)
}

// recursively subdivide cubic Bézier until "flat enough"
func flattenCubicBezier(p0, p1, p2, p3 Point, flatness float64, out *[]Point) {
	// Measure distance of control points from the line p0-p3
	d1 := distPointToLine(p1, p0, p3)
	d2 := distPointToLine(p2, p0, p3)
	if d1 <= flatness && d2 <= flatness {
		// flat enough: approximate with straight line to p3
		*out = append(*out, p3)
		return
	}

	// Subdivide using De Casteljau algorithm
	m01 := lerp(p0, p1, 0.5)
	m12 := lerp(p1, p2, 0.5)
	m23 := lerp(p2, p3, 0.5)
	m012 := lerp(m01, m12, 0.5)
	m123 := lerp(m12, m23, 0.5)
	m0123 := lerp(m012, m123, 0.5)

	// First half: p0, m01, m012, m0123
	flattenCubicBezier(p0, m01, m012, m0123, flatness, out)
	// Second half: m0123, m123, m23, p3
	flattenCubicBezier(m0123, m123, m23, p3, flatness, out)
}

func cross(a, b Point) float64 {
	return a.X*b.Y - a.Y*b.X
}

func almostEqualPoint(a, b Point) bool {
	const eps = 1e-9
	return math.Abs(a.X-b.X) < eps && math.Abs(a.Y-b.Y) < eps
}

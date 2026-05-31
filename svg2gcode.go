package main

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

func normalizeColor(c string) string {
	s := strings.TrimSpace(strings.ToLower(c))
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, "#") {
		s = "#" + s
	}
	return s
}

func extractStrokeColor(strokeAttr, styleAttr string) string {
	if strokeAttr != "" {
		return normalizeColor(strokeAttr)
	}
	if styleAttr == "" {
		return ""
	}
	// style is like "stroke:#000000;stroke-width:2;fill:none"
	parts := strings.Split(styleAttr, ";")
	for _, p := range parts {
		kv := strings.SplitN(strings.TrimSpace(p), ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(kv[0]))
		val := strings.TrimSpace(kv[1])
		if key == "stroke" {
			return normalizeColor(val)
		}
	}
	return ""
}

func extractFillColor(fillAttr, styleAttr string) (fill string, filled bool) {
	if fillAttr != "" {
		v := strings.TrimSpace(strings.ToLower(fillAttr))
		if v == "none" {
			return "none", false
		}
		return normalizeColor(fillAttr), true
	}
	if styleAttr == "" {
		return "", false
	}
	parts := strings.Split(styleAttr, ";")
	for _, p := range parts {
		kv := strings.SplitN(strings.TrimSpace(p), ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(kv[0]))
		val := strings.TrimSpace(strings.ToLower(kv[1]))
		if key == "fill" {
			if val == "" || val == "none" {
				return "none", false
			}
			return normalizeColor(val), true
		}
	}
	return "", false
}

type Path struct {
	Points []Point
	Closed bool
	Stroke string

	Fill   string
	Filled bool
}

type svgRoot struct {
	XMLName   xml.Name      `xml:"svg"`
	Paths     []svgPath     `xml:"path"`
	Polylines []svgPolyLine `xml:"polyline"`
	Polygons  []svgPolyLine `xml:"polygon"`
}

type svgPath struct {
	D      string `xml:"d,attr"`
	Stroke string `xml:"stroke,attr"`
	Fill   string `xml:"fill,attr"`
	Style  string `xml:"style,attr"`
}

type svgPolyLine struct {
	Points string `xml:"points,attr"`
	Stroke string `xml:"stroke,attr"`
	Fill   string `xml:"fill,attr"`
	Style  string `xml:"style,attr"`
}

type Config struct {
	SafeZ      float64
	CutDepth   float64
	StepDown   float64
	CutFeed    float64
	PlungeFeed float64
	Scale      float64

	ToolDia           float64
	Compensation      string // "none", "inside", "outside"
	ConstructionColor string // normalized "#rrggbb", empty = disabled

	PocketMode     string  // "none", "raster"; TODO: spiral
	PocketStepOver float64 // mm; if 0 -> default 0.6 * ToolDia
	PocketRetract  bool    // retract to SafeZ between scanlines

	SvgWidth  float64
	SvgHeight float64

	StartCommand string // GCODE to activate tool / spindle; e.g. M3 S1000

	// only used for reporting
	In  string
	Out string
}

func main() {
	inPath := flag.String("in", "", "input SVG file")
	outPath := flag.String("out", "", "output G-code file (default: stdout)")
	safeZ := flag.Float64("safez", 5.0, "safe Z height (mm)")
	cutZ := flag.Float64("cutz", -1.0, "target cut depth (negative, mm)")
	stepDown := flag.Float64("stepdown", 0.0, "step-down per pass (mm, positive). If 0, do it in a single pass")
	feed := flag.Float64("feed", 300.0, "XY cutting feed rate (mm/min)")
	plunge := flag.Float64("plunge", 120.0, "Z plunge feed rate (mm/min)")
	scale := flag.Float64("scale", 1.0, "coordinate scale factor (SVG units → mm)")
	comp := flag.String("comp", "none", "cutter compensation: none, inside, outside (closed paths only)")
	toolDia := flag.Float64("tooldia", 0.0, "tool diameter in mm (required for inside/outside compensation)")
	pocket := flag.String("pocket", "none", "pocketing mode for filled closed shapes: none, raster")
	stepover := flag.Float64("stepover", 0.0, "pocket stepover (mm). If 0, defaults to 0.6*tooldia when pocketing")
	pocketRetract := flag.Bool("pocketretract", true, "when pocketing, retract to safe Z between scanlines (safer, slower)")
	construction := flag.String("construction", "#0000ff",
		"hex color (e.g. #0000ff) for construction geometry to ignore; empty or 'none' to disable")
	startCommand := flag.String("startcmd", "M3 S1000", "gcode command to start tool/spindel")

	flag.Parse()

	if *inPath == "" {
		fmt.Fprintln(os.Stderr, "error: -in SVG file is required")
		os.Exit(1)
	}

	svgFile, err := os.Open(*inPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening SVG: %v\n", err)
		os.Exit(1)
	}
	defer svgFile.Close()

	paths, w, h, err := parseSVG(svgFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error parsing SVG: %v\n", err)
		os.Exit(1)
	}
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "warning: no paths / polylines / polygons found")
	}

	var out io.Writer = os.Stdout
	if *outPath != "" && *outPath != "-" {
		f, err := os.Create(*outPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating output file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
	}

	cfg := Config{
		SafeZ:        *safeZ,
		CutDepth:     *cutZ,
		StepDown:     *stepDown,
		CutFeed:      *feed,
		PlungeFeed:   *plunge,
		Scale:        *scale,
		ToolDia:      *toolDia,
		Compensation: strings.ToLower(*comp),
		SvgWidth:     w,
		SvgHeight:    h,

		PocketMode:     strings.ToLower(strings.TrimSpace(*pocket)),
		PocketStepOver: *stepover,
		PocketRetract:  *pocketRetract,

		StartCommand: *startCommand,

		In:  *inPath,
		Out: *outPath,
	}

	cc := strings.TrimSpace(*construction)
	if strings.EqualFold(cc, "none") || cc == "" {
		cc = ""
	} else {
		cc = normalizeColor(cc)
	}
	cfg.ConstructionColor = cc

	switch cfg.Compensation {
	case "none", "":
		cfg.Compensation = "none"
	case "inside", "outside":
		if cfg.ToolDia <= 0 {
			fmt.Fprintln(os.Stderr, "error: -tooldia must be > 0 when -comp is inside or outside")
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "error: invalid -comp %q (must be none, inside, outside)\n", *comp)
		os.Exit(1)
	}

	switch cfg.PocketMode {
	case "", "none":
		cfg.PocketMode = "none"
	case "raster":
		if cfg.ToolDia <= 0 {
			fmt.Fprintln(os.Stderr, "error: -tooldia must be > 0 when -pocket is raster")
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "error: invalid -pocket %q (must be none, raster)\n", *pocket)
		os.Exit(1)
	}

	jout, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Println(string(jout))

	if err := writeGcode(out, paths, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error writing G-code: %v\n", err)
		os.Exit(1)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func parsePointsList(s string) ([]Point, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	// Replace commas with spaces, then split on whitespace.
	s = strings.ReplaceAll(s, ",", " ")
	fields := strings.Fields(s)
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("odd number of coordinates in points list")
	}

	pts := make([]Point, 0, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		x, err1 := strconv.ParseFloat(fields[i], 64)
		y, err2 := strconv.ParseFloat(fields[i+1], 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("invalid coordinate pair %q,%q", fields[i], fields[i+1])
		}
		pts = append(pts, Point{X: x, Y: y})
	}
	return pts, nil
}

// parseSimplePath parses a very limited subset of SVG path syntax:
// commands: M/m, L/l, H/h, V/v, Z/z, C/c.
func parseSimplePath(d string) ([]Point, bool, error) {
	tokens := tokenizePathData(d)
	if len(tokens) == 0 {
		return nil, false, nil
	}

	var pts []Point
	var cur Point
	var start Point
	var cmd rune
	closed := false
	i := 0

	flatness := 0.1 // mm tolerance for curve flattening

	for i < len(tokens) {
		tok := tokens[i]

		if isCommand(tok) {
			cmd = rune(tok[0])
			i++
			if cmd == 'Z' || cmd == 'z' {
				if len(pts) > 0 {
					pts = append(pts, start)
					closed = true
				}
				continue
			}
			continue
		}

		if cmd == 0 {
			return nil, false, errors.New("path data must start with a command (M/m)")
		}

		switch cmd {
		case 'M', 'm', 'L', 'l':
			if i+1 >= len(tokens) {
				return nil, false, errors.New("odd number of coordinates after M/L")
			}
			x, err1 := strconv.ParseFloat(tokens[i], 64)
			y, err2 := strconv.ParseFloat(tokens[i+1], 64)
			if err1 != nil || err2 != nil {
				return nil, false, fmt.Errorf("invalid coordinate pair %q,%q", tokens[i], tokens[i+1])
			}

			if cmd == 'm' || cmd == 'l' {
				cur = Point{X: cur.X + x, Y: cur.Y + y}
			} else {
				cur = Point{X: x, Y: y}
			}

			if len(pts) == 0 {
				start = cur
			}
			pts = append(pts, cur)
			i += 2

			// per SVG spec, subsequent coords after first M are treated as L
			if cmd == 'M' {
				cmd = 'L'
			} else if cmd == 'm' {
				cmd = 'l'
			}

		case 'H', 'h':
			x, err := strconv.ParseFloat(tokens[i], 64)
			if err != nil {
				return nil, false, fmt.Errorf("invalid H coordinate %q", tokens[i])
			}
			if cmd == 'h' {
				cur.X += x
			} else {
				cur.X = x
			}
			if len(pts) == 0 {
				start = cur
			}
			pts = append(pts, cur)
			i++

		case 'V', 'v':
			y, err := strconv.ParseFloat(tokens[i], 64)
			if err != nil {
				return nil, false, fmt.Errorf("invalid V coordinate %q", tokens[i])
			}
			if cmd == 'v' {
				cur.Y += y
			} else {
				cur.Y = y
			}
			if len(pts) == 0 {
				start = cur
			}
			pts = append(pts, cur)
			i++

		case 'C', 'c':
			// C/c takes sets of 6 numbers: x1 y1 x2 y2 x y
			for {
				if i+5 >= len(tokens) {
					return nil, false, errors.New("incomplete C/c command; need 6 numbers")
				}
				// If next token is a command, break so outer loop can handle it
				if isCommand(tokens[i]) {
					break
				}

				x1, err1 := strconv.ParseFloat(tokens[i], 64)
				y1, err2 := strconv.ParseFloat(tokens[i+1], 64)
				x2, err3 := strconv.ParseFloat(tokens[i+2], 64)
				y2, err4 := strconv.ParseFloat(tokens[i+3], 64)
				x, err5 := strconv.ParseFloat(tokens[i+4], 64)
				y, err6 := strconv.ParseFloat(tokens[i+5], 64)
				if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || err6 != nil {
					return nil, false, fmt.Errorf("invalid C coordinates near %q", tokens[i])
				}

				var p1, p2, p3 Point
				if cmd == 'c' {
					p1 = Point{X: cur.X + x1, Y: cur.Y + y1}
					p2 = Point{X: cur.X + x2, Y: cur.Y + y2}
					p3 = Point{X: cur.X + x, Y: cur.Y + y}
				} else {
					p1 = Point{X: x1, Y: y1}
					p2 = Point{X: x2, Y: y2}
					p3 = Point{X: x, Y: y}
				}

				// Flatten cubic from cur -> p3
				var seg []Point
				flattenCubicBezier(cur, p1, p2, p3, flatness, &seg)
				if len(seg) == 0 {
					// degenerate, but keep moving
					cur = p3
				} else {
					for _, s := range seg {
						cur = s
						if len(pts) == 0 {
							start = cur
						}
						pts = append(pts, cur)
					}
				}

				i += 6
				if i >= len(tokens) || isCommand(tokens[i]) {
					break
				}
			}

		default:
			return nil, false, fmt.Errorf("unsupported path command %q", string(cmd))
		}
	}

	return pts, closed, nil
}

func isCommand(tok string) bool {
	if len(tok) != 1 {
		return false
	}
	switch tok[0] {
	case 'C', 'c', 'M', 'm', 'L', 'l', 'H', 'h', 'V', 'v', 'Z', 'z':
		return true
	default:
		return false
	}
}

// hasUnsupportedCommands returns true if the path data contains
// any SVG path commands we do not currently implement.
func hasUnsupportedCommands(d string) bool {
	d = strings.TrimSpace(d)
	if d == "" {
		return false
	}

	for _, r := range d {
		// SVG commands are alphabetic characters
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			switch r {
			case 'M', 'm',
				'L', 'l',
				'H', 'h',
				'V', 'v',
				'Z', 'z',
				'C', 'c': // we support cubic Bézier now
				// allowed
			default:
				return true // unsupported command
			}
		}
	}
	return false
}

func tokenizePathData(d string) []string {
	// Insert spaces around command letters and replace commas with spaces
	var b strings.Builder
	commands := "MmLlHhVvZz"
	for _, r := range d {
		if strings.ContainsRune(commands, r) {
			b.WriteRune(' ')
			b.WriteRune(r)
			b.WriteRune(' ')
		} else if r == ',' {
			b.WriteRune(' ')
		} else {
			b.WriteRune(r)
		}
	}
	return strings.Fields(b.String())
}

func writeGcode(w io.Writer, paths []Path, cfg Config) error {
	// Preamble
	fmt.Fprintln(w, "(Generated by svg2gcode)")
	fmt.Fprintln(w, "G21  (units in mm)")
	fmt.Fprintln(w, "G90  (absolute coordinates)")
	fmt.Fprintf(w, "G0 Z%.3f\n", cfg.SafeZ)

	if cfg.StartCommand != "" {
		fmt.Fprintf(w, "%s (spindle on)", cfg.StartCommand)
	}

	if cfg.CutDepth >= 0 {
		return fmt.Errorf("cut depth (cutz) must be negative, got %.3f", cfg.CutDepth)
	}
	targetZ := cfg.CutDepth

	step := cfg.StepDown
	if cfg.StepDown <= 0 {
		step = math.Abs(targetZ) // single pass to depth
	}
	if step <= 0 {
		return fmt.Errorf("invalid stepdown/cutz combination (step=%.6f targetZ=%.6f)", step, targetZ)
	}
	step = math.Abs(step)

	// Separate pocket paths (filled+closed) from profile paths
	var pocketPaths []Path
	var profilePaths []Path

	for _, p := range paths {
		if cfg.PocketMode == "raster" && p.Closed && p.Filled {
			pocketPaths = append(pocketPaths, p)
		} else {
			profilePaths = append(profilePaths, p)
		}
	}

	// Apply cutter compensation ONLY to profile closed paths
	compPaths := make([]Path, 0, len(profilePaths))
	if cfg.Compensation != "none" && cfg.ToolDia > 0 {
		radiusMM := cfg.ToolDia / 2.0
		radiusSVG := radiusMM / cfg.Scale

		for _, p := range profilePaths {
			if !p.Closed {
				compPaths = append(compPaths, p)
				continue
			}
			offsetPts := offsetPolygon(p.Points, radiusSVG, cfg.Compensation)
			if len(offsetPts) < 2 {
				continue
			}
			compPaths = append(compPaths, Path{
				Points: offsetPts,
				Closed: true,
				Stroke: p.Stroke,
				Fill:   p.Fill,
				Filled: p.Filled,
			})
		}
	} else {
		compPaths = profilePaths
	}
	profilePaths = compPaths

	// Travel optimization: reorder paths and rotate/reverse to reduce rapids.
	// Note: this operates in SVG units (pre-scale), which is fine since it's consistent.
	start := Point{X: 0, Y: cfg.SvgHeight} // corresponds to (0,0) in machine coords after Y flip
	pocketPaths = greedyReorderPaths(pocketPaths, start)
	profilePaths = greedyReorderPaths(profilePaths, start)

	for idx, p := range pocketPaths {
		if len(p.Points) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n; Pocket %d fill=%q\n", idx+1, p.Fill)

		// Defaults
		stepoverMM := cfg.PocketStepOver
		if stepoverMM <= 0 {
			stepoverMM = 0.6 * cfg.ToolDia
		}
		stepoverSVG := stepoverMM / cfg.Scale
		toolRadiusSVG := (cfg.ToolDia / 2.0) / cfg.Scale

		segs := RasterPocketSegments(p.Points, stepoverSVG, toolRadiusSVG)
		if len(segs) == 0 {
			continue
		}

		// Move to start at safe Z
		fmt.Fprintf(w, "G0 Z%.3f\n", cfg.SafeZ)

		curZ := 0.0
		for {
			nextZ := curZ - step
			if nextZ < targetZ {
				nextZ = targetZ
			}

			// For each scan segment, plunge/cut (optionally retract between)
			for si, s := range segs {
				ax, ay := writePoint(s.A, cfg)
				bx, by := writePoint(s.B, cfg)

				fmt.Fprintf(w, "G0 X%.3f Y%.3f\n", ax, ay)

				// Plunge
				fmt.Fprintf(w, "G1 Z%.3f F%.3f\n", nextZ, cfg.PlungeFeed)

				// Cut across scanline
				fmt.Fprintf(w, "G1 X%.3f Y%.3f F%.3f\n", bx, by, cfg.CutFeed)

				// Retract between segments if enabled (default true)
				if cfg.PocketRetract || si == len(segs)-1 {
					fmt.Fprintf(w, "G0 Z%.3f\n", cfg.SafeZ)
				}
			}

			if nextZ <= targetZ {
				break
			}
			curZ = nextZ
		}
	}

	for idx, p := range profilePaths {
		if len(p.Points) == 0 {
			continue
		}
		// Skip construction geometry (stroke match)
		if cfg.ConstructionColor != "" && normalizeColor(p.Stroke) == cfg.ConstructionColor {
			continue
		}

		fmt.Fprintf(w, "\n; Path %d stroke=%q\n", idx+1, p.Stroke)

		first := p.Points[0]
		x0, y0 := writePoint(first, cfg)

		fmt.Fprintf(w, "G0 X%.3f Y%.3f\n", x0, y0)
		fmt.Fprintf(w, "G0 Z%.3f\n", cfg.SafeZ)

		curZ := 0.0
		for {
			nextZ := curZ - step
			if nextZ < targetZ {
				nextZ = targetZ
			}

			// plunge at start point
			fmt.Fprintf(w, "G1 Z%.3f F%.3f\n", nextZ, cfg.PlungeFeed)

			// trace the path
			for i := 1; i < len(p.Points); i++ {
				pt := p.Points[i]
				x, y := writePoint(pt, cfg)
				fmt.Fprintf(w, "G1 X%.3f Y%.3f F%.3f\n", x, y, cfg.CutFeed)
			}

			// retract after the pass
			fmt.Fprintf(w, "G0 Z%.3f\n", cfg.SafeZ)

			if nextZ <= targetZ {
				break
			}

			// rapid back to start for next pass
			fmt.Fprintf(w, "G0 X%.3f Y%.3f\n", x0, y0)

			curZ = nextZ
		}

		fmt.Fprintf(w, "G0 Z%.3f\n", cfg.SafeZ)
	}

	fmt.Fprintln(w, "\nM5  (spindle off, if relevant)")
	fmt.Fprintln(w, "M2  (program end)")
	return nil
}

func writePoint(pt Point, cfg Config) (float64, float64) {
	x := pt.X * cfg.Scale
	y := (cfg.SvgHeight - pt.Y) * cfg.Scale
	return x, y
}

// offsetPolygon offsets a closed polygon by delta in SVG units.
// mode is "inside" or "outside" relative to the polygon's interior.
// points may be closed (first == last) or open; result is closed (first == last).
func offsetPolygon(points []Point, delta float64, mode string) []Point {
	if delta == 0 || len(points) < 3 {
		// Nothing to do
		cp := make([]Point, len(points))
		copy(cp, points)
		return cp
	}

	// Remove duplicate closing point if present
	n0 := len(points)
	poly := make([]Point, 0, n0)
	for i, p := range points {
		if i == n0-1 && almostEqualPoint(p, points[0]) {
			break
		}
		poly = append(poly, p)
	}
	n := len(poly)
	if n < 3 {
		cp := make([]Point, len(poly))
		copy(cp, poly)
		return cp
	}

	// Signed area to determine orientation
	area := 0.0
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		area += poly[i].X*poly[j].Y - poly[j].X*poly[i].Y
	}
	area *= 0.5
	if math.Abs(area) < 1e-9 {
		// Degenerate; bail out
		cp := make([]Point, len(poly))
		copy(cp, poly)
		return cp
	}

	dirs := make([]Point, n)
	norms := make([]Point, n)

	for j := 0; j < n; j++ {
		p0 := poly[j]
		p1 := poly[(j+1)%n]
		ex := p1.X - p0.X
		ey := p1.Y - p0.Y
		length := math.Hypot(ex, ey)
		if length == 0 {
			length = 1
		}

		var nIn Point
		if area > 0 {
			// CCW polygon: interior is to the left of edges
			nIn = Point{X: -ey / length, Y: ex / length}
		} else {
			// CW polygon: interior is to the right
			nIn = Point{X: ey / length, Y: -ex / length}
		}

		var nUse Point
		if mode == "inside" {
			nUse = nIn
		} else { // "outside"
			nUse = Point{X: -nIn.X, Y: -nIn.Y}
		}

		dirs[j] = Point{X: ex, Y: ey}
		norms[j] = nUse
	}

	result := make([]Point, 0, n)

	for i := 0; i < n; i++ {
		prev := (i - 1 + n) % n
		cur := i

		pPrev := poly[prev]
		e0 := dirs[prev]
		n0v := norms[prev]
		q0 := Point{X: pPrev.X + n0v.X*delta, Y: pPrev.Y + n0v.Y*delta}

		pCur := poly[cur]
		e1 := dirs[cur]
		n1v := norms[cur]
		q1 := Point{X: pCur.X + n1v.X*delta, Y: pCur.Y + n1v.Y*delta}

		denom := cross(e0, e1)
		if math.Abs(denom) < 1e-9 {
			// Nearly parallel; just take the second offset point
			result = append(result, q1)
			continue
		}

		t := -cross(Point{X: q0.X - q1.X, Y: q0.Y - q1.Y}, e1) / denom
		ix := q0.X + e0.X*t
		iy := q0.Y + e0.Y*t
		result = append(result, Point{X: ix, Y: iy})
	}

	// Close polygon
	if len(result) > 0 {
		result = append(result, result[0])
	}

	return result
}

func dist2(a, b Point) float64 {
	dx := a.X - b.X
	dy := a.Y - b.Y
	return dx*dx + dy*dy
}

// rotateClosedToNearest rotates a CLOSED path (last point duplicates first)
// so that p.Points[0] is the vertex closest to cur.
// Keeps the path closed (last==first).
func rotateClosedToNearest(pts []Point, cur Point) []Point {
	if len(pts) < 4 { // need at least triangle + closing point
		return pts
	}

	// strip closing point
	n := len(pts) - 1
	body := pts[:n]

	best := 0
	bestD := dist2(body[0], cur)
	for i := 1; i < n; i++ {
		d := dist2(body[i], cur)
		if d < bestD {
			bestD = d
			best = i
		}
	}

	if best == 0 {
		return pts
	}

	rot := make([]Point, 0, n+1)
	rot = append(rot, body[best:]...)
	rot = append(rot, body[:best]...)
	rot = append(rot, rot[0]) // close
	return rot
}

func reversePoints(pts []Point) []Point {
	out := make([]Point, len(pts))
	for i := range pts {
		out[i] = pts[len(pts)-1-i]
	}
	return out
}

// optimizePathInPlace chooses reversal (for open) and rotation (for closed)
// so the start is closest to cur.
func optimizePathForStart(p Path, cur Point) Path {
	if len(p.Points) == 0 {
		return p
	}

	if p.Closed {
		p.Points = rotateClosedToNearest(p.Points, cur)
		return p
	}

	// open: consider reversing if end is closer than start
	start := p.Points[0]
	end := p.Points[len(p.Points)-1]
	if dist2(end, cur) < dist2(start, cur) {
		p.Points = reversePoints(p.Points)
	}
	return p
}

// greedyReorderPaths greedily picks the next closest path start,
// performing reversal/rotation to make that start as close as possible.
func greedyReorderPaths(paths []Path, cur Point) []Path {
	remaining := make([]Path, len(paths))
	copy(remaining, paths)

	ordered := make([]Path, 0, len(paths))

	for len(remaining) > 0 {
		bestIdx := 0
		var bestPath Path
		bestPath = optimizePathForStart(remaining[0], cur)
		bestD := dist2(bestPath.Points[0], cur)

		for i := 1; i < len(remaining); i++ {
			cand := optimizePathForStart(remaining[i], cur)
			d := dist2(cand.Points[0], cur)
			if d < bestD {
				bestD = d
				bestIdx = i
				bestPath = cand
			}
		}

		ordered = append(ordered, bestPath)

		// advance cur to path end (so we optimize travels between paths)
		if len(bestPath.Points) > 0 {
			if bestPath.Closed {
				// closed paths end where they start (last==first), so
				// we can treat "end" as start for planning
				cur = bestPath.Points[0]
			} else {
				cur = bestPath.Points[len(bestPath.Points)-1]
			}
		}

		// remove chosen from remaining (swap-delete)
		last := len(remaining) - 1
		remaining[bestIdx] = remaining[last]
		remaining = remaining[:last]
	}

	return ordered
}

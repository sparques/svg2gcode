package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func parseSVG(r io.Reader) (paths []Path, w, h float64, err error) {
	dec := xml.NewDecoder(r)
	var result []Path

	colorStack := []string{""}
	transformStack := []Transform{identityTransform()}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, w, h, fmt.Errorf("decode token: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "svg":
				var vb string
				for _, a := range t.Attr {
					if a.Name.Local == "viewBox" {
						vb = a.Value
					}
				}
				if vb != "" {
					parts := strings.Fields(vb)
					if len(parts) == 4 {
						// viewBox = "minX minY width height"
						w, _ = strconv.ParseFloat(parts[2], 64)
						h, _ = strconv.ParseFloat(parts[3], 64)
					}
				}
			case "g":
				// stroke / style on group
				var strokeAttr, styleAttr, transformAttr string
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "stroke":
						strokeAttr = a.Value
					case "style":
						styleAttr = a.Value
					case "transform":
						transformAttr = a.Value
					}
				}
				groupColor := extractStrokeColor(strokeAttr, styleAttr)
				if groupColor == "" {
					groupColor = colorStack[len(colorStack)-1]
				}
				colorStack = append(colorStack, groupColor)

				parentT := transformStack[len(transformStack)-1]
				groupT := parseTransformAttr(transformAttr)
				transformStack = append(transformStack, parentT.Mul(groupT))

			case "path":
				currentGroupColor := colorStack[len(colorStack)-1]
				currentT := transformStack[len(transformStack)-1]

				var raw svgPath
				if err := dec.DecodeElement(&raw, &t); err != nil {
					return nil, w, h, fmt.Errorf("decode <path>: %w", err)
				}
				d := strings.TrimSpace(raw.D)
				if d == "" {
					continue
				}
				if hasUnsupportedCommands(d) {
					continue
				}
				pts, closed, err := parseSimplePath(d)
				if err != nil {
					return nil, w, h, fmt.Errorf("parse path d=%q: %w", truncate(d, 40), err)
				}
				if len(pts) == 0 {
					continue
				}
				// apply current transform
				for i := range pts {
					pts[i] = currentT.Apply(pts[i])
				}

				strokeCol := extractStrokeColor(raw.Stroke, raw.Style)
				if strokeCol == "" {
					strokeCol = currentGroupColor
				}

				result = append(result, Path{
					Points: pts,
					Closed: closed,
					Stroke: strokeCol,
				})

			case "polyline":
				currentGroupColor := colorStack[len(colorStack)-1]
				currentT := transformStack[len(transformStack)-1]

				var raw svgPolyLine
				if err := dec.DecodeElement(&raw, &t); err != nil {
					return nil, w, h, fmt.Errorf("decode <polyline>: %w", err)
				}
				pts, err := parsePointsList(raw.Points)
				if err != nil {
					return nil, w, h, fmt.Errorf("parse polyline points: %w", err)
				}
				if len(pts) == 0 {
					continue
				}
				for i := range pts {
					pts[i] = currentT.Apply(pts[i])
				}
				strokeCol := extractStrokeColor(raw.Stroke, raw.Style)
				if strokeCol == "" {
					strokeCol = currentGroupColor
				}

				result = append(result, Path{
					Points: pts,
					Closed: false,
					Stroke: strokeCol,
				})

			case "polygon":
				currentGroupColor := colorStack[len(colorStack)-1]
				currentT := transformStack[len(transformStack)-1]

				var raw svgPolyLine
				if err := dec.DecodeElement(&raw, &t); err != nil {
					return nil, w, h, fmt.Errorf("decode <polygon>: %w", err)
				}
				pts, err := parsePointsList(raw.Points)
				if err != nil {
					return nil, w, h, fmt.Errorf("parse polygon points: %w", err)
				}
				if len(pts) == 0 {
					continue
				}
				for i := range pts {
					pts[i] = currentT.Apply(pts[i])
				}
				if !almostEqualPoint(pts[0], pts[len(pts)-1]) {
					pts = append(pts, pts[0])
				}
				strokeCol := extractStrokeColor(raw.Stroke, raw.Style)
				if strokeCol == "" {
					strokeCol = currentGroupColor
				}

				result = append(result, Path{
					Points: pts,
					Closed: true,
					Stroke: strokeCol,
				})
			}

		case xml.EndElement:
			if t.Name.Local == "g" {
				if len(colorStack) > 1 {
					colorStack = colorStack[:len(colorStack)-1]
				}
				if len(transformStack) > 1 {
					transformStack = transformStack[:len(transformStack)-1]
				}
			}
		}
	}

	return result, w, h, nil
}

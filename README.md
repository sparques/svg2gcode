# 🛠️ svg2gcode

A small, strict, predictable command-line tool that converts **2D SVG geometry** into **G-code** for CNC engraving, stencil cutting, PCB isolation routing, simple 2.5D operations, and template milling.

`svg2gcode` is intentionally minimal:
it does not try to be a CAM suite, CAD package, or GUI.
It takes geometry in, produces G-code out, and stays out of your way.

This was made after experiencing the joy that is pcb2gcode and the misery that is FreeCAD's CAM workbench.

---

## ✨ Features

* Converts **SVG paths**, **polylines**, and **polygons** to G-code
* Handles **nested `<g>` groups** with **inherited stroke color**
* Supports **translate() transforms** (and ignores unsupported ones cleanly)
* Flattens **cubic Bézier curves** (`C/c`) to straight segments
* Optional **cutter compensation** (`inside`, `outside`) for closed paths
* Avoids paths of a specified **construction color** (default: `#0000ff`)
* Generates **absolute** G-code (`G90`) in **millimeters** (`G21`)
* Handles step-down passes for deeper cuts
* Correctly flips the Y-axis so origin matches CNC convention (bottom-left)
* Produces deterministic output suitable for 3018-class machines

---

## 📦 Installation

```bash
git clone https://github.com/sparques/svg2gcode
cd svg2gcode
go build -o svg2gcode
```

---

## 🚀 Basic Usage

```bash
svg2gcode -in drawing.svg -out cut.nc
```

This reads `drawing.svg` and emits G-code to `cut.nc`.

---

## ⚙️ Options

| Flag            | Meaning                                          |
| --------------- | ------------------------------------------------ |
| `-in`           | Input SVG file (required)                        |
| `-out`          | Output G-code file (default: stdout)             |
| `-safez`        | Safe travel Z height (default: 5 mm)             |
| `-cutz`         | Cutting depth (must be negative, e.g. `-1.2`)    |
| `-stepdown`     | Step-down amount per pass (0 = single pass)      |
| `-feed`         | XY feed rate (mm/min)                            |
| `-plunge`       | Z plunge rate (mm/min)                           |
| `-scale`        | Scale factor (SVG units → mm)                    |
| `-comp`         | Cutter compensation: `none`, `inside`, `outside` |
| `-tooldia`      | Tool diameter (required for compensation)        |
| `-construction` | Color of construction geometry to ignore         |

### Example: milling a stencil with ⅛" endmill

```bash
svg2gcode \
  -in logo.svg \
  -out logo.nc \
  -cutz -0.8 \
  -stepdown 0.4 \
  -feed 300 \
  -plunge 100 \
  -comp outside \
  -tooldia 3.175
```

### Example: ignoring construction geometry

```bash
svg2gcode -in panel.svg -construction "#ff0000"
```

All paths stroked in red will be skipped.

---

## 🧠 How SVG Coordinates Are Mapped

SVG coordinate systems define (0,0) at the **top-left**, where Y
increases downward.

CNC machines expect (0,0) at the **bottom-left** with Y increasing
upward.

svg2gcode performs:

```
x' = x * scale
y' = (svgHeight - y) * scale
```

Where `svgHeight` comes from:

* `viewBox="minX minY width height"` → uses `height`
* If no viewBox is present → Y-axis flip still occurs, but scaling may require manual tuning

---

## ✂️ Supported SVG Features

| Feature              | Supported? | Notes                              |
| -------------------- | ---------- | ---------------------------------- |
| `<path>`             | ✔️         | Supports M, L, H, V, C, Z          |
| `<polyline>`         | ✔️         | Open paths                         |
| `<polygon>`          | ✔️         | Auto-closed                        |
| Cubic Béziers        | ✔️         | Flattened recursively (`C/c`)      |
| Relative commands    | ✔️         | (`m`, `l`, etc.)                   |
| Nested groups        | ✔️         | Inherits stroke + transform        |
| translate(x,y)       | ✔️         | Only transform supported right now |
| stroke:* in style="" | ✔️         | Extracted and normalized           |

---

## 🚫 Unsupported SVG Features (Gracefully Ignored)

* Arcs (`A/a`)
* Quadratic Béziers (`Q/q`, `T/t`)
* Ellipses and circles
* Paths that use unsupported commands
* rotate(), scale(), matrix(), skew() transforms
* Fill rules (`fill:*`) — only strokes matter
* Stylesheets / external CSS
* Anything not strictly geometry

Unsupported paths simply **do not appear** in the G-code output.
They do not cause errors unless partially parsed.

Most objects can be made visible to svg2gcode by converting them to a path 
from within your SVG editing application, e.g. inkscape.

---

## 🔧 Cutter Compensation Details

When using:

```
-comp inside
-comp outside
```

svg2gcode:

* Computes polygon orientation by signed area
* Determines the interior normal
* Offsets each edge by ± tool radius
* Intersects adjacent offset edges
* Produces a new closed polygon

Algorithm lives in `offsetPolygon()`  

Open paths **cannot** be compensated. They are passed through unchanged.

---

## 🛑 Limitations

These are deliberate — svg2gcode is meant to be predictable, not magical.

* Does not sort paths or optimize travel
* Does not raise/lower spindle automatically (only emits M5/M2)
* Does not detect self-intersecting polygons
* Ignores stroke width (only geometry matters)
* Does not perform pocketing / engraving fill (but might later)
* Does not support Z in SVG (this is a strict 2D → G-code mapper)
* Does not try to combine collinear segments
* No automatic tabbing, dogbones, or CAM features

If you need a CAM suite, use one.
If you want **precise, hand-controlled geometry**, this tool is for you.

---

## 🧭 Roadmap

Possible future enhancements:

* Support for rotate/scale transforms
* Arcs (`A`) → G2/G3 emissions
* Quadratic Béziers
* Optional path sorting (nearest-neighbor)
* Annotating layers with depth metadata

---

## 📚 Source Structure

* `svg2gcode.go` — CLI, flags, G-code generation  
* `parsesvg.go` — XML walker, group handling, transforms  
* `geometry.go` — Bézier flattening, transforms, offset math  

#!/usr/bin/env python3
"""Generate the Axon Companion app icon.

Derived from the dashboard's brand mark (`web/src/styles.css` `.brand-mark`):
a glowing signal node in AXON's teal/indigo, on the dashboard's near-black.
Here it gains the axons that give the product its name — a node with
processes reaching out to smaller nodes, which is also the second-brain
graph the app is a window onto.

Generated rather than hand-drawn so the palette stays locked to the
dashboard's, and so a colour change is a one-line edit rather than a
round trip through a design tool.

Usage: Scripts/make_icon.py [out.png] [size]
"""

import math
import sys

from PIL import Image, ImageDraw, ImageFilter

# AXON signal palette — identical values to web/src/styles.css.
TEAL = (47, 224, 207)
INDIGO = (111, 124, 242)
VIOLET = (176, 124, 240)
CORE = (138, 246, 236)
BACKDROP_TOP = (16, 20, 30)
BACKDROP_BOTTOM = (8, 10, 16)

# Supersample, then downsample once at the end: circles and thin strokes
# drawn directly at final size alias badly at the small icon variants.
SUPERSAMPLE = 4


def lerp(a, b, t):
    return tuple(round(x + (y - x) * t) for x, y in zip(a, b))


def backdrop(size):
    """Vertical gradient plate, matching the dashboard's background."""
    image = Image.new("RGB", (size, size))
    draw = ImageDraw.Draw(image)
    for y in range(size):
        draw.line([(0, y), (size, y)], fill=lerp(BACKDROP_TOP, BACKDROP_BOTTOM, y / size))
    return image


def radial_glow(size, center, radius, color, falloff=2.2):
    """A soft radial glow as its own layer, composited additively."""
    layer = Image.new("L", (size, size), 0)
    pixels = layer.load()
    cx, cy = center
    box = range(max(0, int(cx - radius)), min(size, int(cx + radius) + 1))
    for x in box:
        for y in range(max(0, int(cy - radius)), min(size, int(cy + radius) + 1)):
            distance = math.hypot(x - cx, y - cy)
            if distance >= radius:
                continue
            intensity = (1 - distance / radius) ** falloff
            pixels[x, y] = int(255 * intensity)
    tint = Image.new("RGB", (size, size), color)
    return tint, layer


def draw_icon(size):
    work = size * SUPERSAMPLE
    image = backdrop(work).convert("RGBA")

    centre = (work / 2, work / 2)
    core_radius = work * 0.098

    # Axons: processes reaching from the soma to terminal nodes. The angles are
    # deliberately irregular — a radially symmetric burst reads as a sun or a
    # loading spinner, not a neuron — but no two share a ray either, since a
    # pair within ~15 degrees becomes one thick smudge at 32pt.
    branches = [
        (-64, 0.33, 0.044, TEAL),
        (14, 0.36, 0.049, INDIGO),
        (88, 0.30, 0.037, VIOLET),
        (152, 0.35, 0.042, TEAL),
        (206, 0.27, 0.033, VIOLET),
        (264, 0.31, 0.038, INDIGO),
    ]

    lines = Image.new("RGBA", (work, work), (0, 0, 0, 0))
    draw = ImageDraw.Draw(lines)
    for degrees, reach, node_scale, color in branches:
        radians = math.radians(degrees)
        end = (
            centre[0] + math.cos(radians) * work * reach,
            centre[1] + math.sin(radians) * work * reach,
        )
        start = (
            centre[0] + math.cos(radians) * core_radius * 0.85,
            centre[1] + math.sin(radians) * core_radius * 0.85,
        )
        draw.line([start, end], fill=color + (185,), width=int(work * 0.011))
        node = work * node_scale
        draw.ellipse(
            [end[0] - node, end[1] - node, end[0] + node, end[1] + node],
            fill=color + (255,),
        )

    # Bloom the whole tracery, then lay the crisp version back on top so the
    # nodes glow without going soft.
    bloom = lines.filter(ImageFilter.GaussianBlur(work * 0.016))
    image.alpha_composite(bloom)
    image.alpha_composite(lines)

    # The soma: a bright core with a halo, echoing the dashboard's pulsing dot.
    for radius_scale, color, falloff in ((0.34, INDIGO, 2.6), (0.20, TEAL, 2.0)):
        tint, mask = radial_glow(work, centre, work * radius_scale, color, falloff)
        image.paste(tint, (0, 0), mask)

    draw = ImageDraw.Draw(image)
    steps = 48
    for step in range(steps, 0, -1):
        t = step / steps
        radius = core_radius * t
        # Offset highlight, like the dashboard mark's `circle at 35% 30%`.
        colour = lerp(TEAL, CORE, (1 - t) ** 0.8)
        draw.ellipse(
            [
                centre[0] - radius - core_radius * 0.10 * (1 - t),
                centre[1] - radius - core_radius * 0.14 * (1 - t),
                centre[0] + radius - core_radius * 0.10 * (1 - t),
                centre[1] + radius - core_radius * 0.14 * (1 - t),
            ],
            fill=colour + (255,),
        )

    # macOS applies its own mask to the icon plate, so the art is full-bleed
    # square here — rounding it ourselves would double-round it.
    return image.resize((size, size), Image.LANCZOS).convert("RGB")


def main():
    out = sys.argv[1] if len(sys.argv) > 1 else "build/icon_1024.png"
    size = int(sys.argv[2]) if len(sys.argv) > 2 else 1024
    draw_icon(size).save(out)
    print(f"wrote {out} ({size}x{size})")


if __name__ == "__main__":
    main()

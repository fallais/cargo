"""Draws the two README diagrams: assets/how-it-works.svg and dtc-anatomy.svg.

Edit the labels here rather than in the SVGs, then run from the repository root:

    python3 assets/make-diagrams.py assets

Each SVG carries light and dark colours and follows the reader's theme.
"""
import sys
out = sys.argv[1]

FONT = 'system-ui,-apple-system,"Segoe UI",Helvetica,Arial,sans-serif'
MONO = 'ui-monospace,SFMono-Regular,Menlo,Consolas,monospace'

STYLE = f"""<style>
  .panel {{ fill:#f6f8fa; stroke:#d1d9e0; }}
  .card  {{ fill:#ffffff; stroke:#d1d9e0; }}
  .car   {{ fill:#0969da; fill-opacity:.04; stroke:#0969da; stroke-opacity:.28; stroke-dasharray:5 4; }}
  .t  {{ fill:#1f2328; font:600 15px {FONT}; }}
  .s  {{ fill:#59636e; font:400 12.5px {FONT}; }}
  .l  {{ fill:#59636e; font:500 12px {FONT}; }}
  .k  {{ fill:#59636e; font:600 11px {FONT}; letter-spacing:.06em; }}
  .m  {{ font-family:{MONO}; font-weight:700; }}
  .ink   {{ fill:#1f2328; }}
  .mute  {{ fill:#59636e; }}
  .wire  {{ stroke:#8c959f; stroke-width:1.6; fill:none; }}
  .bus   {{ stroke:#0969da; stroke-width:4; stroke-linecap:round; }}
  .busfill {{ fill:#0969da; }}
  .icon  {{ fill:none; stroke:#59636e; stroke-width:1.6; stroke-linejoin:round; }}
  .h1 {{ --c:#0969da; }} .h2 {{ --c:#8250df; }} .h3 {{ --c:#1a7f37; }}
  .h4 {{ --c:#bc4c00; }} .h5 {{ --c:#cf222e; }}
  .tint   {{ fill:var(--c); fill-opacity:.10; stroke:var(--c); stroke-opacity:.45; }}
  .hue    {{ fill:var(--c); }}
  .huestroke {{ stroke:var(--c); stroke-width:1.6; fill:none; }}
  @media (prefers-color-scheme: dark) {{
    .panel {{ fill:#151b23; stroke:#3d444d; }}
    .card  {{ fill:#0d1117; stroke:#3d444d; }}
    .car   {{ fill:#4493f8; fill-opacity:.05; stroke:#4493f8; stroke-opacity:.35; }}
    .t, .ink {{ fill:#e6edf3; }}
    .s, .l, .k, .mute {{ fill:#9198a1; }}
    .wire  {{ stroke:#656c76; }}
    .bus   {{ stroke:#4493f8; }}
    .busfill {{ fill:#4493f8; }}
    .icon  {{ stroke:#9198a1; }}
    .h1 {{ --c:#4493f8; }} .h2 {{ --c:#ab7df8; }} .h3 {{ --c:#3fb950; }}
    .h4 {{ --c:#f0883e; }} .h5 {{ --c:#f85149; }}
    .tint {{ fill-opacity:.14; stroke-opacity:.55; }}
  }}
</style>"""

def svg(w, h, body, title):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" '
            f'role="img" aria-label="{title}">\n<title>{title}</title>\n{STYLE}\n'
            f'<rect class="panel" x="0.5" y="0.5" width="{w-1}" height="{h-1}" rx="16"/>\n{body}</svg>\n')

def esc(s): return s.replace("&", "&amp;").replace("<", "&lt;")

def pill(x_right, y, text, hue, cw=7.2, pad=10, h=22):
    w = len(text) * cw + 2 * pad
    x = x_right - w
    return (f'<g class="{hue}"><rect class="tint" x="{x:.1f}" y="{y}" width="{w:.1f}" height="{h}" rx="{h/2}"/>'
            f'<text class="hue m" x="{x + w/2:.1f}" y="{y + h/2 + 4}" text-anchor="middle" font-size="11.5">{esc(text)}</text></g>\n')

# ---------------------------------------------------------------- overview
W, H = 1040, 440
b = []
CY = 232  # the row the port and the body controller share

# diagnostic tool
b.append(f'<rect class="card" x="28" y="184" width="140" height="96" rx="12"/>')
b.append('<rect class="icon" x="84" y="197" width="28" height="19" rx="2.5"/>'
         '<path class="icon" d="M78 220 h40 l-4 4 h-32 z"/>')
b.append('<text class="t" x="98" y="250" text-anchor="middle">Diagnostic tool</text>')
b.append('<text class="s" x="98" y="267" text-anchor="middle">laptop or phone</text>')

# adapter
b.append(f'<rect class="card" x="268" y="184" width="120" height="96" rx="12"/>')
b.append('<rect class="icon" x="320" y="196" width="16" height="6" rx="1.5"/>'
         '<rect class="icon" x="314" y="202" width="28" height="20" rx="4"/>'
         '<circle class="busfill" cx="335" cy="212" r="2.4"/>')
b.append('<text class="t" x="328" y="250" text-anchor="middle">ELM327</text>')
b.append('<text class="s" x="328" y="267" text-anchor="middle">adapter</text>')

# links
b.append('<defs><marker id="a" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto">'
         '<path d="M0 0 L10 5 L0 10 z" class="mute"/></marker></defs>')
b.append(f'<path class="wire" d="M168 {CY} H262" marker-end="url(#a)"/>')
b.append(f'<text class="l" x="215" y="{CY-10}" text-anchor="middle">USB · Bluetooth</text>')
b.append(f'<path class="wire" d="M388 {CY} H435"/>')

# the car
b.append('<rect class="car" x="448" y="24" width="564" height="392" rx="16"/>')
b.append('<text class="k" x="470" y="48">THE CAR</text>')

# port
b.append(f'<rect class="card" x="435" y="{CY-20}" width="26" height="40" rx="5"/>')
for i in range(3):
    b.append(f'<circle class="mute" cx="448" cy="{CY-9+i*9}" r="1.6"/>')
b.append(f'<text class="l" x="448" y="{CY+38}" text-anchor="middle">OBD-II</text>')
b.append(f'<text class="l" x="448" y="{CY+53}" text-anchor="middle">port</text>')

# bus
BX = 520
b.append(f'<path class="wire" d="M461 {CY} H{BX}"/>')
b.append(f'<line class="bus" x1="{BX}" y1="70" x2="{BX}" y2="390"/>')
b.append(f'<text class="k" transform="translate({BX-12} 150) rotate(-90)" text-anchor="middle">CAN BUS</text>')

ecus = [
    ("Engine",         "fuel, glow plugs, turbo",      "OBD-II + UDS", "h4"),
    ("ABS / ESP",      "brakes, stability",            "UDS",          "h1"),
    ("Body control",   "lights, locks, tyre pressure", "UDS",          "h1"),
    ("Airbag",         "airbags, seatbelts",           "UDS",          "h1"),
    ("Power steering", "steering assist",              "KWP2000",      "h2"),
]
X0, CW, CH, GAP, Y0 = 560, 262, 56, 16, 60
rows = []
for i, (name, sub, proto, hue) in enumerate(ecus):
    y = Y0 + i * (CH + GAP)
    cy = y + CH / 2
    rows.append(cy)
    b.append(f'<path class="wire" d="M{BX} {cy} H{X0}"/><circle class="busfill" cx="{BX}" cy="{cy}" r="4"/>')
    b.append(f'<rect class="card" x="{X0}" y="{y}" width="{CW}" height="{CH}" rx="10"/>')
    b.append(f'<text class="t" x="{X0+16}" y="{y+24}">{esc(name)}</text>')
    b.append(f'<text class="s" x="{X0+16}" y="{y+43}">{esc(sub)}</text>')
    b.append(pill(X0 + CW - 12, y + 10, proto, hue))

# one ECU's fault memory
cy = rows[2]
b.append(f'<path class="wire" d="M{X0+CW} {cy} H{X0+CW+30}" stroke-dasharray="3 3"/>')
FX = X0 + CW + 30
b.append(f'<g class="h5"><rect class="tint" x="{FX}" y="{cy-34}" width="150" height="68" rx="10"/></g>')
b.append(f'<text class="k" x="{FX+14}" y="{cy-13}">FAULT MEMORY</text>')
b.append(f'<text class="ink m" x="{FX+14}" y="{cy+6}" font-size="15">C1A60-7B</text>')
b.append(f'<g class="h5"><circle class="hue" cx="{FX+18}" cy="{cy+20}" r="3.5"/></g>')
b.append(f'<text class="s" x="{FX+28}" y="{cy+24}">failing now</text>')

open(f"{out}/how-it-works.svg", "w").write(svg(W, H, "\n".join(b) + "\n",
    "A diagnostic tool reaches the car's ECUs through an adapter plugged into the OBD-II port. "
    "Each ECU sits on the CAN bus, speaks its own protocol and keeps its own fault memory."))

# ---------------------------------------------------------------- anatomy
W, H = 1000, 284
b = []
segs = [  # text, width, hue, caption
    ("C",   44, "h1", ("System", "P powertrain · C chassis", "B body · U network")),
    ("1",   44, "h2", ("Who defines it", "0 the standard (SAE)", "1 the manufacturer")),
    ("A60", 96, "h3", ("Component", "which part or function", "here: tyre pressure")),
    ("7B",  70, "h4", ("Failure type", "how it failed", "7B: low fluid level")),
    ("A9",  70, "h5", ("Status", "failing now, confirmed,", "warning lamp requested")),
]
gaps = [8, 8, 22, 30]       # after C, 1, A60 (dash), 7B (dot)
seps = {2: "-", 3: "·"}
total = sum(s[1] for s in segs) + sum(gaps)
x = (W - total) / 2
PY, PH = 36, 58
centres = []
for i, (text, w, hue, _) in enumerate(segs):
    b.append(f'<g class="{hue}"><rect class="tint" x="{x:.1f}" y="{PY}" width="{w}" height="{PH}" rx="12"/>'
             f'<text class="hue m" x="{x + w/2:.1f}" y="{PY + 40}" text-anchor="middle" font-size="32">{text}</text></g>')
    centres.append(x + w / 2)
    x += w
    if i < len(gaps):
        if i in seps:
            b.append(f'<text class="mute m" x="{x + gaps[i]/2:.1f}" y="{PY + 39}" text-anchor="middle" font-size="28">{seps[i]}</text>')
        x += gaps[i]

CWID, CGAP, CY0, CHT = 176, 16, 150, 116
cx0 = (W - (5 * CWID + 4 * CGAP)) / 2
for i, (text, w, hue, (title, d1, d2)) in enumerate(segs):
    left = cx0 + i * (CWID + CGAP)
    mid = left + CWID / 2
    px = centres[i]
    b.append(f'<g class="{hue}"><path class="huestroke" d="M{px:.1f} {PY+PH} C{px:.1f} {PY+PH+30} {mid:.1f} {CY0-30} {mid:.1f} {CY0}"/>'
             f'<circle class="hue" cx="{px:.1f}" cy="{PY+PH}" r="3"/></g>')
    b.append(f'<rect class="card" x="{left:.1f}" y="{CY0}" width="{CWID}" height="{CHT}" rx="12"/>')
    b.append(f'<g class="{hue}"><text class="hue m" x="{mid:.1f}" y="{CY0+30}" text-anchor="middle" font-size="18">{text}</text></g>')
    b.append(f'<text class="t" x="{mid:.1f}" y="{CY0+56}" text-anchor="middle" font-size="14">{esc(title)}</text>')
    b.append(f'<text class="s" x="{mid:.1f}" y="{CY0+79}" text-anchor="middle">{esc(d1)}</text>')
    b.append(f'<text class="s" x="{mid:.1f}" y="{CY0+97}" text-anchor="middle">{esc(d2)}</text>')

open(f"{out}/dtc-anatomy.svg", "w").write(svg(W, H, "\n".join(b) + "\n",
    "Anatomy of the trouble code C1A60-7B with status A9: system, who defines it, component, "
    "failure type and status."))

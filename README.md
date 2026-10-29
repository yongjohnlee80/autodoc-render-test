# autodoc-render-test

An [AutoDoc](https://github.com/yongjohnlee80/autodoc) plugin for measuring rendering: a docked,
resizable card that draws a 3D scene on a pixel surface (plugin protocol 10) and shows the frame
rate, frame times and memory as it runs.

The scene is golib's `gui/examples/scene3d`: a textured, lit crate turning over a tiled floor. It
is drawn by one of two renderers, switched with `r`:

- **vulkan**: on the GPU, on a Vulkan device of its own, into device memory; the frame is copied
  into a cached host buffer and from there into the card's shared-memory buffer. A plugin hands
  AutoDoc shared memory, not GPU images, so the copy is part of the cost.
- **cpu**: in Go, straight into the card's buffer, one band of rows per core.

**Which is better?** At a 60 Hz display both keep up, so the shown frame rate and frame times are
the same; that's AutoDoc's side, and the point of the card. They differ in what the plugin
spends:

| 1496×874 frame (RTX 5060 Ti, Ryzen 9 3900X) | render | the plugin's CPU |
|---|---|---|
| vulkan | about 2.9 ms | low: it mostly waits on the GPU and copies |
| cpu | about 4.6 ms | high: every core rasterizes for that time |

The CPU renderer's cost grows with the card's area and steals cores from AutoDoc; the GPU's
hardly does. Watch the `plugin: CPU` figure when you switch with `r`.

It starts on Vulkan and falls back to the CPU, with a note on the overlay, when no device takes it.

## Install

In AutoDoc's GUI (`autodoc --gui`): **Plugins › Add from a git URL…**, then

```
https://github.com/yongjohnlee80/autodoc-render-test
```

AutoDoc clones it and asks before anything runs. On **Yes, at my own risk** it builds the plugin
and lists **Plugins › Render test (3D)**. A plugin runs as you.

The build needs Go 1.26.9 or newer, a C compiler and the Vulkan headers and loader
(`vulkan-headers` and `vulkan-icd-loader` on Arch, `libvulkan-dev` on Debian and Ubuntu), since
the Vulkan renderer is cgo. Pixel surfaces are Linux's today; elsewhere the card shows a line of
text.

## Using it

The card docks at the right (or the left: **Plugins › Manage plugins… › Place**). Drag its title
row or border to move or resize it; the surface follows each resize. In the terminal (`--ui`) it
shows a line saying to open it in the GUI.

| Key | |
|---|---|
| r | switch the renderer: vulkan ↔ cpu |
| u | paced (the default: the next frame once AutoDoc showed the last) ↔ unpaced (as fast as AutoDoc credits buffers: a stress test, most frames superseded) |
| space | pause and resume rendering, to watch AutoDoc idle |
| o | hide or show the overlay |
| Esc | close the card (the plugin keeps running) |

## What the overlay shows

Updated once a second; the graph is the latest 180 frames shown.

| Line | Meaning |
|---|---|
| renderer, surface | the renderer and GPU, paced or unpaced, the surface in device pixels, its scale and the buffer format AutoDoc gave |
| shown fps | frames AutoDoc drew this second, and those it **superseded** (a newer one came first) or **did not show** (the card was hidden). `rendered` is what the plugin drew |
| frame time (shown) | the time between frames AutoDoc drew: p50, p95 and the worst, against the 16.7 ms budget of a 60 Hz display |
| render | how long the renderer took to draw a frame |
| present→shown | from the plugin presenting a frame to AutoDoc drawing it |
| host | AutoDoc's process (the plugin's parent): resident memory, swap and CPU |
| plugin | this plugin's CPU (100% is one core), resident memory, Go heap and garbage collections |

The graph's bars are green within one refresh (16.7 ms), amber within two, red beyond; the white
line is the budget.

Paced, the plugin draws one frame per refresh AutoDoc shows; unpaced, it draws as fast as AutoDoc
credits buffers, and the extra frames are superseded. Either way AutoDoc draws every refresh
while the card runs: pause it (space) or close it when you're not measuring.

package scene

/*
#cgo LDFLAGS: -lvulkan
#include <stdlib.h>
#include "vulkan_linux.h"
*/
import "C"

import (
	_ "embed"
	"errors"
	"fmt"
	"unsafe"
)

// The shaders are prebuilt, as in golib's gui/examples/scene3d:
//
//go:generate glslc --target-env=vulkan1.1 -O shaders/scene.vert -o shaders/scene.vert.spv
//go:generate glslc --target-env=vulkan1.1 -O shaders/scene.frag -o shaders/scene.frag.spv

var (
	//go:embed shaders/scene.vert.spv
	vertSPV []byte
	//go:embed shaders/scene.frag.spv
	fragSPV []byte
)

// Vulkan renders the scene on the GPU, on a VkDevice of its own. Draw reads the frame back into
// the caller's buffer (AutoDoc's shared memory): the copy is the price of that path. NewTarget
// makes an image the device exports as a DMA-BUF instead, which AutoDoc shows with no copy.
type Vulkan struct {
	s      *C.s3d
	device string
	target *C.s3d_target // the read-back target, kept while the size holds
	w, h   int
	pixels []byte // the read-back rows, tightly packed RGBA
}

// NewVulkan opens the first Vulkan device that renders the scene and uploads it.
func NewVulkan() (*Vulkan, error) {
	var errBuf [256]C.char
	cerr := func() error { return errors.New(C.GoString(&errBuf[0])) }
	var uuid [16]byte
	var s *C.s3d
	if C.s3d_new((*C.uint8_t)(unsafe.Pointer(&uuid[0])), 0, &s, &errBuf[0], C.int(len(errBuf))) != 0 {
		C.s3d_free(s)
		return nil, fmt.Errorf("vulkan: %w", cerr())
	}
	ms := newMesh()
	atlas := newAtlas()
	if C.s3d_scene(s, unsafe.Pointer(&ms.verts[0]), C.uint32_t(len(ms.verts)),
		C.uint32_t(ms.floor[0]), C.uint32_t(ms.floor[1]), C.uint32_t(ms.crate[0]), C.uint32_t(ms.crate[1]),
		(*C.uint8_t)(unsafe.Pointer(&atlas.Pix[0])), C.int(atlasW), C.int(atlasH),
		unsafe.Pointer(&vertSPV[0]), C.uint32_t(len(vertSPV)), unsafe.Pointer(&fragSPV[0]), C.uint32_t(len(fragSPV)),
		&errBuf[0], C.int(len(errBuf))) != 0 {
		C.s3d_free(s)
		return nil, fmt.Errorf("vulkan scene: %w", cerr())
	}
	return &Vulkan{s: s, device: C.GoString(C.s3d_device_name(s))}, nil
}

// Name is "vulkan".
func (v *Vulkan) Name() string { return "vulkan" }

// Device is the GPU's name.
func (v *Vulkan) Device() string { return v.device }

// Draw renders the scene at time t on the GPU and copies it into dst (w by h, pitch bytes a row,
// BGRA when bgra is set). One goroutine calls it at a time.
func (v *Vulkan) Draw(t float64, dst []byte, w, h, pitch int, bgra bool) error {
	var errBuf [256]C.char
	if v.target == nil || v.w != w || v.h != h {
		C.s3d_target_free(v.s, v.target)
		v.target, v.w, v.h = nil, 0, 0
		var tg *C.s3d_target
		if C.s3d_readback_new(v.s, C.int(w), C.int(h), C.S3D_RGBA8, &tg, &errBuf[0], C.int(len(errBuf))) != 0 {
			C.s3d_target_free(v.s, tg)
			return fmt.Errorf("vulkan read-back target: %s", C.GoString(&errBuf[0]))
		}
		v.target, v.w, v.h = tg, w, h
		v.pixels = make([]byte, w*h*4)
	}
	draws := drawsAt(t, imagePt(w, h))
	if C.s3d_render(v.s, v.target, (*C.float)(unsafe.Pointer(&draws[0])), &errBuf[0], C.int(len(errBuf))) != 0 {
		return fmt.Errorf("vulkan render: %s", C.GoString(&errBuf[0]))
	}
	if C.s3d_read(v.s, v.target, (*C.uint8_t)(unsafe.Pointer(&v.pixels[0])), &errBuf[0], C.int(len(errBuf))) != 0 {
		return fmt.Errorf("vulkan read: %s", C.GoString(&errBuf[0]))
	}
	copyRows(dst, pitch, v.pixels, w, h, bgra)
	return nil
}

// Close frees the target and the device.
func (v *Vulkan) Close() {
	C.s3d_target_free(v.s, v.target)
	C.s3d_free(v.s)
	v.target, v.s = nil, nil
}

// vkTarget is an image the device renders into and exports.
type vkTarget struct {
	v     *Vulkan
	t     *C.s3d_target
	w, h  int
	fd    int
	pitch int
}

// NewTarget makes a w by h image exported as a LINEAR, one-plane DMA-BUF starting at its first
// byte; an error when the device exports none such.
func (v *Vulkan) NewTarget(w, h int, bgra bool) (Target, error) {
	var errBuf [256]C.char
	format := C.int(C.S3D_RGBA8)
	if bgra {
		format = C.int(C.S3D_BGRA8)
	}
	var mods [64]C.uint64_t
	n := int(C.s3d_modifiers(v.s, format, &mods[0], C.int(len(mods))))
	linear := false
	for _, m := range mods[:n] {
		linear = linear || m == 0
	}
	if !linear {
		return nil, fmt.Errorf("vulkan: %s exports no LINEAR DMA-BUF it can render to (%d other modifiers)", v.device, n)
	}
	var tg *C.s3d_target
	var ex C.s3d_export
	only := C.uint64_t(0) // DRM_FORMAT_MOD_LINEAR
	if C.s3d_target_new(v.s, C.int(w), C.int(h), format, &only, 1, &tg, &ex, &errBuf[0], C.int(len(errBuf))) != 0 {
		C.s3d_target_free(v.s, tg)
		return nil, fmt.Errorf("vulkan exported target: %s", C.GoString(&errBuf[0]))
	}
	if ex.offset != 0 || ex.modifier != 0 {
		C.s3d_target_free(v.s, tg)
		return nil, fmt.Errorf("vulkan exported target: offset %d, modifier %#x; want a LINEAR plane at 0", uint32(ex.offset), uint64(ex.modifier))
	}
	return &vkTarget{v: v, t: tg, w: w, h: h, fd: int(ex.fd), pitch: int(ex.pitch)}, nil
}

func (t *vkTarget) FD() int    { return t.fd }
func (t *vkTarget) Pitch() int { return t.pitch }

// Render draws the scene at time t into the image and waits for it.
func (t *vkTarget) Render(at float64) error {
	var errBuf [256]C.char
	draws := drawsAt(at, imagePt(t.w, t.h))
	if C.s3d_render(t.v.s, t.t, (*C.float)(unsafe.Pointer(&draws[0])), &errBuf[0], C.int(len(errBuf))) != 0 {
		return fmt.Errorf("vulkan render: %s", C.GoString(&errBuf[0]))
	}
	return nil
}

// Free frees the image and closes its DMA-BUF. Free every target before the renderer's Close.
func (t *vkTarget) Free() {
	if t.t != nil {
		C.s3d_target_free(t.v.s, t.t)
		t.t = nil
	}
}

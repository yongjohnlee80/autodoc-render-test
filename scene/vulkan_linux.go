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

// Vulkan renders the scene on the GPU, on a VkDevice of its own, and reads each frame back into
// the caller's buffer: a plugin hands AutoDoc shared memory, not GPU images, so the copy is the
// price of drawing on the GPU here.
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

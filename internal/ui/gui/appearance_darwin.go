package gui

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>
static void hopseshAppearance(int mode) {
 NSApp.appearance = mode == 1 ? [NSAppearance appearanceNamed:NSAppearanceNameAqua] :
                     mode == 2 ? [NSAppearance appearanceNamed:NSAppearanceNameDarkAqua] : nil;
}
*/
import "C"

import "github.com/wailsapp/wails/v3/pkg/application"

func nativeAppearance(mode string) {
	value := C.int(0)
	if mode == "light" {
		value = 1
	}
	if mode == "dark" {
		value = 2
	}
	application.InvokeSync(func() { C.hopseshAppearance(value) })
}

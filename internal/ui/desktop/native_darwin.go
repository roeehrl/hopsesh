package desktop

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit -framework Carbon
#import <AppKit/AppKit.h>
#import <Carbon/Carbon.h>
static int hsAccessory(int accessory) {
 NSApplicationActivationPolicy policy = accessory ? NSApplicationActivationPolicyAccessory : NSApplicationActivationPolicyRegular;
 if ([NSApp activationPolicy] == policy) return 1;
 [NSApp setActivationPolicy:policy];
 return [NSApp activationPolicy] == policy;
}
static int hsLoginLaunch(void) {
 NSAppleEventDescriptor *event = [[NSAppleEventManager sharedAppleEventManager] currentAppleEvent];
 return [[event paramDescriptorForKeyword:keyAEPropData] enumCodeValue] == keyAELaunchedAsLogInItem;
}
*/
import "C"
import (
	"errors"
	"os"
	"slices"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func capabilities() Capabilities { return Capabilities{Tray: true, HideApp: true} }
func setAppHidden(_ *application.WebviewWindow, hidden bool) error {
	ok := false
	application.InvokeSync(func() {
		n := 0
		if hidden {
			n = 1
		}
		ok = C.hsAccessory(C.int(n)) != 0
	})
	if !ok {
		return errors.New("macOS could not change Dock visibility")
	}
	return nil
}
func loginLaunch() bool {
	if slices.Contains(os.Args, "--background") {
		return true
	}
	var yes bool
	application.InvokeSync(func() { yes = C.hsLoginLaunch() != 0 })
	return yes
}
func loginOptions() application.AutostartOptions {
	return application.AutostartOptions{Arguments: []string{"--background"}}
}

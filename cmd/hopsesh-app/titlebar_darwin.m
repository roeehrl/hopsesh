#import <Cocoa/Cocoa.h>

double hopseshTrafficLightsEnd(void *window) {
    if (!window) return 0;
    NSButton *button = [(NSWindow *)window standardWindowButton:NSWindowZoomButton];
    if (!button || !button.superview) return 0;
    NSRect frame = [button.superview convertRect:button.frame toView:nil];
    return NSMaxX(frame);
}

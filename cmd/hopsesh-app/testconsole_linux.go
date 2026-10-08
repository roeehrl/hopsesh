//go:build e2e && linux

package main

/*
#cgo pkg-config: gtk4 webkitgtk-6.0
#include <gtk/gtk.h>
#include <webkit/webkit.h>
static void hopsesh_test_console(GtkWidget *widget) {
    if (!widget) return;
    if (WEBKIT_IS_WEB_VIEW(widget)) {
        webkit_settings_set_enable_write_console_messages_to_stdout(webkit_web_view_get_settings(WEBKIT_WEB_VIEW(widget)), TRUE);
    }
    for (GtkWidget *child = gtk_widget_get_first_child(widget); child; child = gtk_widget_get_next_sibling(child)) {
        hopsesh_test_console(child);
    }
}
*/
import "C"

import "github.com/wailsapp/wails/v3/pkg/application"

// Native-only failures (CSP, module loads, GTK) need the browser console in CI.
// This is compiled only into the isolated end-to-end test build.
func testConsole(w *application.WebviewWindow) {
	application.InvokeSync(func() { C.hopsesh_test_console((*C.GtkWidget)(w.NativeWindow())) })
}

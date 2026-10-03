# dmgbuild settings for the hopsesh disk image (used by scripts/build-macos-app.sh).
# The window: hopsesh.app on the left, a link to Applications on the right, the background
# (background.png and background@2x.png, joined into one TIFF) showing the drag between them.
# hopsesh.app must stay at the root of the volume: the app's updater copies <mount>/hopsesh.app.
#
#   dmgbuild -s settings.py -D app=<hopsesh.app> -D background=<background.tiff> \
#     -D icon=<hopsesh.icns> hopsesh <out.dmg>

app = defines["app"]  # noqa: F821 (defines is provided by dmgbuild)

format = "UDZO"
filesystem = "HFS+"
files = [app]
symlinks = {"Applications": "/Applications"}
icon = defines.get("icon")  # noqa: F821  the volume's icon in Finder
background = defines["background"]  # noqa: F821

# 380 points of content (the background's height) plus the title bar.
window_rect = ((200, 140), (540, 408))
default_view = "icon-view"
show_status_bar = False
show_tab_view = False
show_toolbar = False
show_pathbar = False
show_sidebar = False
show_icon_preview = False
include_icon_view_settings = True
arrange_by = None
icon_size = 128
text_size = 12
icon_locations = {"hopsesh.app": (140, 170), "Applications": (400, 170)}

// The hopsesh plugin's entry, as OpenCode 2.x loads a plugin folder: Plugin.define from the
// host's @opencode/plugin (OpenCode provides it; nothing is installed), with the logic in
// hopsesh.js and hopsesh's settings in config.json beside this file.
import { Plugin } from "@opencode/plugin"
import { setup } from "./hopsesh.js"

export default Plugin.define({
  id: "hopsesh",
  setup: (ctx) => setup(ctx, new URL("./config.json", import.meta.url)),
})

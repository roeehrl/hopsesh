import { api, on } from "./core.js";
import { setAppearance } from "./terminal/appearance.js";

// Subscribe before reading; an older startup reply must not overwrite a live change.
let revision = 0;
on("hopsesh:appearance", mode => { revision++; setAppearance(mode); });
const started = revision;
api("Appearance").then(mode => { if (revision === started) setAppearance(mode); }).catch(console.error);

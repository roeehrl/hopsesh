#!/usr/bin/env python3
"""Build the narrated cut of the launch video from a captionless picture and one voice take.

  narrate.py plan TAKE MARKS VIDEO CAPS.json        split the take at its pauses, place each line
                                                    on its beat, write the caption timings
  narrate.py mix  TAKE MARKS VIDEO CAPS.json CAPDIR OUT.mp4 [MUSIC]
                                                    mix voice (+ ducked music) and overlay the
                                                    rendered captions (capture/captions.mjs)
The script is demo/voiceover.txt. Needs ffmpeg.
"""
import json, os, re, subprocess, sys

HERE = os.path.dirname(os.path.abspath(__file__))
CHUNK = 64  # caption characters before splitting a line at a comma or sentence end


def script():
    rows = []
    for ln in open(os.path.join(HERE, "voiceover.txt"), encoding="utf-8"):
        if ln.startswith("#") or not ln.strip():
            continue
        beat, spoken, cap = (ln.rstrip("\n").split("\t") + [""])[:3]
        rows.append((beat, spoken, spoken if cap == "" else cap))
    return rows


def dur(p):
    return float(subprocess.check_output(["ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", p]))


def segments(take):
    log = subprocess.run(["ffmpeg", "-i", take, "-af", "silencedetect=noise=-38dB:d=0.7", "-f", "null", "-"],
                         capture_output=True, text=True).stderr
    starts = [float(x) for x in re.findall(r"silence_start: ([\d.]+)", log)]
    ends = [float(x) for x in re.findall(r"silence_end: ([\d.]+)", log)]
    total, segs, cur = dur(take), [], 0.0
    for s, e in zip(starts, ends):
        if s > cur + 0.2:
            segs.append((max(0, cur - 0.05), s + 0.08))
        cur = e
    if total > cur + 0.2:
        segs.append((max(0, cur - 0.05), total))
    return segs


def place(take, marks):
    rows, segs = script(), segments(take)
    if len(segs) != len(rows):
        sys.exit(f"the take has {len(segs)} lines, voiceover.txt has {len(rows)}")
    out, free = [], 0.0
    for (a, b), (beat, spoken, cap) in zip(segs, rows):
        at = max(marks[beat] + (0.35 if beat == "title" else 0.25), free)
        out.append({"beat": beat, "a": a, "b": b, "at": at, "spoken": spoken, "caption": cap})
        free = at + (b - a) + 0.25
    return out


def chunks(text):
    parts = re.findall(r"[^,.?!]+[,.?!]?\s*", text)
    res, cur = [], ""
    for p in parts:
        if cur and len(cur) + len(p) > CHUNK:
            res.append(cur.strip())
            cur = ""
        cur += p
    if cur.strip():
        res.append(cur.strip())
    return res


def plan(take, marks_path, video, caps_path):
    marks = json.load(open(marks_path))
    lines, caps = place(take, marks), []
    for ln in lines:
        print(f"{ln['beat']:9s} {ln['b']-ln['a']:4.1f}s at {ln['at']:5.2f}s (beat {marks[ln['beat']]:5.2f}s)")
        if ln["caption"] == "-":
            continue
        parts, span, t = chunks(ln["caption"]), ln["b"] - ln["a"], ln["at"]
        total = sum(len(p) for p in parts)
        for p in parts:
            d = span * len(p) / total
            caps.append({"start": round(t, 2), "end": round(t + d, 2), "text": p})
            t += d
    # hold each caption until just before the next one (at most 0.6 s after speech)
    for c, n in zip(caps, caps[1:] + [None]):
        c["end"] = round(min(c["end"] + 0.6, n["start"] - 0.05) if n else c["end"] + 0.6, 2)
    json.dump(caps, open(caps_path, "w"), indent=1)
    vlen = dur(video)
    if lines[-1]["at"] + lines[-1]["b"] - lines[-1]["a"] > vlen:
        print(f"WARNING: the voice runs past the video ({vlen:.1f}s)")
    return lines


def mix(take, marks_path, video, caps_path, capdir, out, music=None):
    lines = place(take, json.load(open(marks_path)))
    caps = json.load(open(caps_path))
    vlen = dur(video)
    inputs = ["-i", video, "-i", take]
    fc = []
    for i, ln in enumerate(lines):
        ms = int(ln["at"] * 1000)
        fc.append(f"[1:a]atrim={ln['a']:.3f}:{ln['b']:.3f},asetpts=PTS-STARTPTS,adelay={ms}|{ms}[v{i}]")
    fc.append("".join(f"[v{i}]" for i in range(len(lines))) +
              f"amix=inputs={len(lines)}:normalize=0,apad,atrim=0:{vlen:.2f}[vo]")
    n = 2
    if music:
        inputs += ["-i", music]
        fc.append(f"[{n}:a]atrim=0:{vlen:.2f},volume=0.22,afade=t=in:d=1.0,afade=t=out:st={vlen-2.0:.2f}:d=2.0[mu]")
        fc.append("[vo]asplit[vo1][vo2];[mu][vo1]sidechaincompress=threshold=0.04:ratio=6:attack=20:release=350[duck]")
        fc.append("[duck][vo2]amix=inputs=2:normalize=0,loudnorm=I=-16:TP=-1.5:LRA=11[aout]")
        n += 1
    else:
        fc.append("[vo]loudnorm=I=-16:TP=-1.5:LRA=11[aout]")
    last = "0:v"
    for i, c in enumerate(caps):
        d = c["end"] - c["start"]
        inputs += ["-loop", "1", "-t", f"{d:.2f}", "-i", os.path.join(capdir, f"cap-{i:02d}.png")]
        fc.append(f"[{n}:v]format=rgba,fade=in:st=0:d=0.25:alpha=1,fade=out:st={max(0, d-0.3):.2f}:d=0.3:alpha=1,"
                  f"setpts=PTS-STARTPTS+{c['start']:.2f}/TB[c{i}]")
        fc.append(f"[{last}][c{i}]overlay=0:0:eof_action=pass[o{i}]")
        last, n = f"o{i}", n + 1
    subprocess.check_call(["ffmpeg", "-v", "error", "-y", *inputs, "-filter_complex", ";".join(fc),
                           "-map", f"[{last}]", "-map", "[aout]", "-c:v", "libx264", "-preset", "slow", "-crf", "20",
                           "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "192k", "-ar", "48000",
                           "-movflags", "+faststart", "-t", f"{vlen:.2f}", out])
    print("wrote", out)


if __name__ == "__main__":
    if sys.argv[1] == "plan":
        plan(*sys.argv[2:6])
    else:
        mix(*sys.argv[2:])

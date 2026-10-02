// Package audit keeps an append-only JSONL log (one file per day in the state folder) of
// every remote command, copy, install and undo. The undo journal itself is kept by
// package engine.
package audit

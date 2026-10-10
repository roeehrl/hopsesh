package runtime

import "runtime"

// Resources is an on-demand process sample, including any GUI hosted in the
// same process. It exposes neither goroutine stacks nor filesystem paths and
// adds no monitoring timer. Heap allocation is distinct from OS resident memory.
type Resources struct {
	Goroutines  int    `json:"goroutines"`
	HeapBytes   uint64 `json:"heapBytes"`
	HeapObjects uint64 `json:"heapObjects"`
}

func readResources() Resources {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return Resources{Goroutines: runtime.NumGoroutine(), HeapBytes: memory.HeapAlloc, HeapObjects: memory.HeapObjects}
}

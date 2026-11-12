package claude

// Claude Code's parent links are not always complete or in file order. Two shapes occur
// in real transcripts, and both walkers (the full reader and the tail preview) repair
// them the same way before following parentUuid:
//
//   - A child written just before its parent. A tool result can land one record ahead of
//     the tool call it answers, and a reply ahead of the attachment it follows; their
//     timestamps are the same millisecond. writeOrder puts the parent first when it
//     follows within reorderWindow chain records. A parent further ahead, or a cycle,
//     is left for the walk to refuse.
//   - A parent that was never written. Some Claude Code versions leave the first reply
//     of a turn naming a record they never saved (hundreds per long session), a full disk
//     loses records mid-turn, and a compaction boundary's logical parent can be missing
//     too. gaps continues such a record from the main-chain record just before it, if
//     that record has no child of its own. Before this, a walk stopped at the first such
//     record, silently dropping all earlier history; a later transfer then saw its own
//     earlier records gone and refused as "history was compacted or rewound".

// link is the part of a transcript record the chain walk follows.
type link struct {
	uuid     string
	parent   string // parentUuid, or logicalParentUuid on a compaction boundary
	explicit bool   // parentUuid itself is set: a boundary's logical parent is not
	chain    bool   // a chain record with a uuid, on a sidechain or not
	main     bool   // a chain record with a uuid, not on a sidechain
}

func chainLink(typ, uuid string, parent *string, logical string, sidechain bool) link {
	l := link{uuid: uuid, chain: isChain(typ) && uuid != ""}
	l.main = l.chain && !sidechain
	if parent != nil {
		l.parent, l.explicit = *parent, *parent != ""
	} else {
		l.parent = logical
	}
	return l
}

// reorderWindow bounds how far ahead a parent may be written: Claude Code's own write
// races put it on the next record.
const reorderWindow = 4

// writeOrder returns record indexes with each early child moved after its parent, when
// the parent follows within reorderWindow main-chain records. Everything else keeps
// file order.
func writeOrder(ls []link) []int {
	first := map[string]int{}
	for i, l := range ls {
		if l.main {
			if _, ok := first[l.uuid]; !ok {
				first[l.uuid] = i
			}
		}
	}
	order := make([]int, 0, len(ls))
	placed := make([]bool, len(ls))
	defined := map[string]bool{}
	var place func(i, depth int)
	place = func(i, depth int) {
		if placed[i] {
			return
		}
		if l := ls[i]; l.main && l.explicit && !defined[l.parent] && depth < reorderWindow {
			if j, ok := first[l.parent]; ok && j > i && !placed[j] && mainBetween(ls, i, j) < reorderWindow {
				place(j, depth+1)
			}
		}
		placed[i] = true
		order = append(order, i)
		if ls[i].main {
			defined[ls[i].uuid] = true
		}
	}
	for i := range ls {
		place(i, 0)
	}
	return order
}

func mainBetween(ls []link, i, j int) int {
	n := 0
	for k := i + 1; k < j; k++ {
		if ls[k].main {
			n++
		}
	}
	return n
}

// gaps maps each main-chain record whose parent appears nowhere before it to the
// main-chain record just before it, when that record has no child of its own: the
// conversation continued from there across records Claude Code never wrote. A parent
// that is only written later (a real forward reference) is not a gap, except on a
// compaction boundary, whose logical parent a fork copies after it. With no record
// before it, or one that already has a child, the record still starts the history.
func gaps(ls []link) map[int]int {
	anywhere := map[string]bool{}
	children := map[string]int{}
	for _, l := range ls {
		if l.chain {
			anywhere[l.uuid] = true // a sidechain record was written too
		}
		if l.main {
			if l.parent != "" {
				children[l.parent]++
			}
		}
	}
	out := map[int]int{}
	defined := map[string]bool{}
	prev := -1
	for i, l := range ls {
		if !l.main {
			continue
		}
		if l.parent != "" && !defined[l.parent] && (!anywhere[l.parent] || !l.explicit) && prev >= 0 && children[ls[prev].uuid] == 0 {
			out[i] = prev
		}
		defined[l.uuid] = true
		prev = i
	}
	return out
}

package journal

import "errors"

// Acknowledgment tracks a remote protocol receipt separately from filesystem
// receipts. A delivered snapshot is never mistaken for a confirmed remote write.
type Acknowledgment struct {
	Machine   string `json:"machine"`
	Transport string `json:"transport"`
	Peer      string `json:"peer"`
	Operation string `json:"operation"`
	Applied   bool   `json:"applied"`
}

func (j *Journal) QueueAcknowledgment(a Acknowledgment) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, old := range j.Acknowledgments {
		if old.Peer == a.Peer && old.Operation == a.Operation {
			if old.Machine != a.Machine || old.Transport != a.Transport {
				return errors.New("acknowledgment binding changed")
			}
			return nil
		}
	}
	j.Acknowledgments = append(j.Acknowledgments, a)
	return j.saveLocked()
}
func (j *Journal) CompleteAcknowledgment(peer, operation, machine, remoteJournal string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	found := false
	for i, a := range j.Acknowledgments {
		if a.Peer == peer && a.Operation == operation && a.Machine == machine {
			j.Acknowledgments[i].Applied = true
			found = true
			break
		}
	}
	if !found {
		return errors.New("no matching durable acknowledgment")
	}
	for _, r := range j.Remote {
		if r.Machine == machine && r.ID == remoteJournal {
			return j.saveLocked()
		}
	}
	j.Remote = append(j.Remote, Remote{Machine: machine, ID: remoteJournal})
	return j.saveLocked()
}
func (j *Journal) PendingAcknowledgments() bool {
	for _, a := range j.Acknowledgments {
		if !a.Applied {
			return true
		}
	}
	return false
}

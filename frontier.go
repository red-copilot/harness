package harness

import (
	"fmt"
	"sort"
	"strings"
)

// FrontierKind classifies an exploration item.
type FrontierKind string

const (
	FrontierCandidate FrontierKind = "candidate"
	FrontierQuestion  FrontierKind = "question"
)

// FrontierStatus tracks whether an exploration item still needs attention.
type FrontierStatus string

const (
	FrontierOpen FrontierStatus = "open"
	FrontierDone FrontierStatus = "done"
)

// FrontierItem is a bounded exploration hint. It is never proof of progress.
type FrontierItem struct {
	ID        string         `json:"id"`
	Kind      FrontierKind   `json:"kind"`
	Text      string         `json:"text"`
	Priority  int            `json:"priority"`
	Status    FrontierStatus `json:"status"`
	SourceSeq int64          `json:"source_seq,omitempty"`
}

// Frontier is a deterministic, read-only projection of the next exploration
// items for a State revision.
type Frontier struct {
	StateRevision int64          `json:"state_revision"`
	Items         []FrontierItem `json:"items"`
}

// ExplorationContext is the bounded context the controller sends to the agent
// before its next action.
type ExplorationContext struct {
	StateRevision      int64                    `json:"state_revision"`
	Objective          string                   `json:"objective"`
	RecentObservations []ExplorationObservation `json:"recent_observations,omitempty"`
	Frontier           Frontier                 `json:"frontier"`
	Focus              *FrontierItem            `json:"focus,omitempty"`
}

// ProjectExplorationFrontier derives candidate targets and unresolved
// verification questions from State. It deliberately does not persist a
// second mutable frontier.
func ProjectExplorationFrontier(state ExplorationState, limit int) Frontier {
	if limit <= 0 {
		limit = 3
	}
	items := make([]FrontierItem, 0, len(state.Items))
	for _, item := range state.Items {
		if item.Status == FrontierOpen {
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		items = seedFrontierItems(state.Task, state.Target)
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Priority != items[j].Priority {
			return items[i].Priority > items[j].Priority
		}
		if items[i].SourceSeq != items[j].SourceSeq {
			return items[i].SourceSeq > items[j].SourceSeq
		}
		return items[i].ID < items[j].ID
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return Frontier{StateRevision: state.Revision, Items: append([]FrontierItem(nil), items...)}
}

// BuildExplorationContext creates the bounded state/frontier view consumed by
// an agent before its next action.
func BuildExplorationContext(state ExplorationState, limit int) ExplorationContext {
	frontier := ProjectExplorationFrontier(state, limit)
	observations := state.Observations
	if len(observations) > 3 {
		observations = observations[len(observations)-3:]
	}
	context := ExplorationContext{
		StateRevision:      state.Revision,
		Objective:          state.Task.Objective,
		RecentObservations: append([]ExplorationObservation(nil), observations...),
		Frontier:           frontier,
	}
	if len(frontier.Items) > 0 {
		focus := frontier.Items[0]
		context.Focus = &focus
	}
	return context
}

// FormatExplorationContext renders a compact prompt section. Observations are
// explicitly marked untrusted so the agent cannot promote them to success.
func FormatExplorationContext(context ExplorationContext) string {
	var out strings.Builder
	out.WriteString("Exploration update.\n")
	if context.Objective != "" {
		fmt.Fprintf(&out, "Objective: %s\n", context.Objective)
	}
	if len(context.RecentObservations) > 0 {
		out.WriteString("Recent untrusted observations (they do not prove success):\n")
		for _, observation := range context.RecentObservations {
			status := "ok"
			if observation.Error {
				status = "error"
			}
			fmt.Fprintf(&out, "- [%s; %s] %s\n", observation.Tool, status, truncateExplorationText(observation.Text))
		}
	}
	if len(context.Frontier.Items) > 0 {
		out.WriteString("Frontier:\n")
		for i, item := range context.Frontier.Items {
			fmt.Fprintf(&out, "%d. [%s] %s\n", i+1, item.Kind, item.Text)
		}
	}
	if context.Focus != nil {
		fmt.Fprintf(&out, "Focus for the next action: %s\n", context.Focus.Text)
	} else {
		out.WriteString("Focus for the next action: state what evidence is still missing.\n")
	}
	out.WriteString("Use the focus to choose one concrete next action. Do not claim objective success; only the Verifier or backend completion can do that.")
	return out.String()
}

func targetCandidates(target Target) []FrontierItem {
	var items []FrontierItem
	for i, address := range target.Addresses {
		if strings.TrimSpace(address) == "" {
			continue
		}
		items = append(items, FrontierItem{
			ID: fmt.Sprintf("target:%d:%s", i, address), Kind: FrontierCandidate, Status: FrontierOpen,
			Text: "Investigate authorized target " + address, Priority: 80,
		})
	}
	if len(items) == 0 && target.ID != "" {
		items = append(items, FrontierItem{
			ID: "target:" + target.ID, Kind: FrontierCandidate, Status: FrontierOpen,
			Text: "Investigate authorized target " + target.ID, Priority: 80,
		})
	}
	return items
}

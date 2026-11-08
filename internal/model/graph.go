package model

import "strings"

// topoOrder orders the nodes so that every node comes after the nodes it
// depends on, keeping the given order where the dependencies allow, and
// reports one cycle as the names along it when there is one. deps maps a
// node to the nodes it reads, restricted to nodes of the graph.
func topoOrder(nodes []string, deps map[string][]string) (order []string, cycle []string) {
	index := map[string]int{}
	for i, n := range nodes {
		index[n] = i
	}
	indegree := map[string]int{}
	readers := map[string][]string{}
	for _, n := range nodes {
		for _, d := range deps[n] {
			if _, ok := index[d]; !ok {
				continue
			}
			indegree[n]++
			readers[d] = append(readers[d], n)
		}
	}
	var ready []string
	for _, n := range nodes {
		if indegree[n] == 0 {
			ready = append(ready, n)
		}
	}
	done := map[string]bool{}
	for len(ready) > 0 {
		// The earliest declared ready node goes next, so that the order
		// is stable and reads like the class.
		best := 0
		for i := range ready {
			if index[ready[i]] < index[ready[best]] {
				best = i
			}
		}
		n := ready[best]
		ready = append(ready[:best], ready[best+1:]...)
		order = append(order, n)
		done[n] = true
		for _, r := range readers[n] {
			indegree[r]--
			if indegree[r] == 0 {
				ready = append(ready, r)
			}
		}
	}
	if len(order) == len(nodes) {
		return order, nil
	}
	// Every node left is on or behind a cycle; walk from the first one
	// along dependencies that are themselves undone until a node repeats.
	for _, start := range nodes {
		if done[start] {
			continue
		}
		seen := map[string]int{}
		path := []string{}
		n := start
		for {
			if at, ok := seen[n]; ok {
				return order, append(path[at:], n)
			}
			seen[n] = len(path)
			path = append(path, n)
			next := ""
			for _, d := range deps[n] {
				if _, inGraph := index[d]; inGraph && !done[d] {
					next = d
					break
				}
			}
			if next == "" {
				break
			}
			n = next
		}
	}
	return order, nil
}

// cycleText renders a cycle for a message: a depends on b depends on a.
func cycleText(cycle []string) string {
	return strings.Join(cycle, " reads ")
}

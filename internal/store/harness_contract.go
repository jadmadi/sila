package store

// CountMemoryWrites counts memo/lesson/case facts attributable to a session.
// An empty sessionRef counts any manual or session-attributed record.
func (s *Store) CountMemoryWrites(projectSel, sessionRef string) (int, error) {
	facts, err := s.FactsByProject(projectSel)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, f := range facts {
		switch f.Kind {
		case "memo", "lesson", "case":
		default:
			continue
		}
		if sessionRef == "" || f.SessionRef == sessionRef {
			n++
		}
	}
	return n, nil
}

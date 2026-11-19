package stores

// Every number that tunes store resolution lives in this file. Change one
// only together with the specification and the boundary rows of the
// decision tests.
const (
	// minStoreScore is the similarity at which a printed name alone links
	// a receipt to one of the user's stores (resolution step 5).
	minStoreScore = 0.70

	// minStoreLead is how far the best store must score above the best
	// different store to be linked by similarity. Without a clear lead the
	// user chooses, as in matching: a wrong silent link is worse than a
	// flagged new store.
	minStoreLead = 0.08

	// maxStoreCandidates is how many store and alias names the
	// nearest-names query returns.
	maxStoreCandidates = 20

	// scoreTolerance lets a score that rounds to a threshold reach it.
	scoreTolerance = 1e-9
)

// Pure model-decision API of the single-file managed extension.
// The implementation lives in the exact source Go installs: importing a sibling from
// that installed file would break standalone Pi loading and the bundled runtime.
// These are re-exports, not a second reducer, generated copy or test implementation.
export {
	newClientModelState, deriveTransition, reduce, parseDecision, parseBootstrap,
	canonicalSerialize, snapshotDecisionHeaders,
} from "./pi_extension";
export type {
	ClientModelState, ModelDecisionConfig, ClientModelDecision, EffectTarget, EffectToken,
	ControllerCommit, ClientEvent,
} from "./pi_extension";

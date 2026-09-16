// Package harness is the AI "game master": it observes the world
// through game.Server's GM readout, prompts a local OpenAI-compatible
// LLM, and applies the parsed actions back into the game.
//
// The loop lives in loop.go (ticker → observe → prompt → tolerant JSON
// parse → verbs). The contract: the harness is the optional component —
// an unreachable or rambling LLM logs and idles; the game keeps serving
// (tests/harness_mock CI holds that line).
package harness

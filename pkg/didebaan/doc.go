// Package didebaan is the public, embeddable API surface for the Didebaan
// telemetry collector: the stable contract between an AI coding agent's input
// adapter and the collector core.
//
// Didebaan reads an AI coding agent's activity, normalizes it into the
// OpenTelemetry GenAI semantic conventions (gen_ai.*), and exports it over OTLP
// (see the adr/ directory). This package defines the two types that contract
// rests on: the normalized [Event] that adapters produce, and the [Adapter]
// interface each agent's reader implements. The collector implementation lives
// under internal/ and is not part of this contract.
package didebaan

// dn-dedicated deliberately has ZERO third-party dependencies.
//
// The rest of the Revival stack uses gorilla/mux, logrus, prometheus and
// google/uuid. This tool does not, for one practical reason: it must build and
// run on an operator's machine with no module cache and no network. A dedicated
// server you cannot build offline is not much of a dedicated server. Everything
// those libraries provided here (routing, JSON logging, UUIDv4) is a few dozen
// lines of standard library.
//
// Consequence to know about: the HTTP API this exposes is byte-compatible with
// game-manager's, but it is not the same code, so a change to game-manager's
// routes has to be mirrored here by hand. See internal/api.
module dn-dedicated

go 1.24

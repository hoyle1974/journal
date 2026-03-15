package infra

// SystemCollection is the Firestore collection name for system documents (sync state, debounce, etc.).
const SystemCollection = "_system"

// DeployMetaDoc is the _system document ID that stores last-deployed commit for "run once per deploy" detection.
const DeployMetaDoc = "deploy_meta"

// DreamRunDoc is the _system document ID for the current/latest async dream run (lock, status, phase, log).
const DreamRunDoc = "dream_run"

// OnboardingDoc is the _system document ID for first-run onboarding (seeded_at, status, version).
const OnboardingDoc = "onboarding"

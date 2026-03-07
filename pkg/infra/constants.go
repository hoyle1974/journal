package infra

// SystemCollection is the Firestore collection name for system documents (sync state, debounce, etc.).
const SystemCollection = "_system"

// DeployMetaDoc is the _system document ID that stores last-deployed commit for "run once per deploy" detection.
const DeployMetaDoc = "deploy_meta"

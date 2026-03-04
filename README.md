# JOT: Agentic Second Brain

JOT is a single-user, AI-driven personal assistant and knowledge graph built in Go. It operates on the principle of separating **"Gold"** (permanent facts, relationship details, project milestones) from **"Gravel"** (temporary logistics, conversational filler, one-off errands).

It continuously ingests raw, chronological daily logs (Episodic Memory) and uses Google's Gemini LLMs to distill them into a highly structured, vector-searchable Knowledge Graph (Semantic Memory).

## Features

* **Multi-Channel Ingestion:** Log entries via CLI, Twilio SMS, or by typing into a synced Google Doc.
* **Agentic Query System:** Ask questions about your life, projects, or the world. JOT uses an agentic loop with tools for semantic search, Wikipedia, web search, and mathematical/date calculations.
* **The Dreamer (Nightly Consolidation):** A background chron job that reviews the last 24 hours of logs using a "Committee of Minds" (Anthropologist, Architect, Executive, Philosopher, Self-Modeler) to extract facts and update your permanent persona profile.
* **Goal Planning:** Ask JOT to plan a project, and it will break it down into structured phases and track them in the knowledge graph.
* **Automated Janitor & Pulse Audits:** Background processes that garbage-collect low-significance, stale memories and proactively flag forgotten goals or relationships.

## Architecture & Tech Stack

* **Language:** Go 1.26
* **AI/LLM:** Google Gemini (2.5 Flash) for reasoning and data extraction; Vertex AI (`text-embedding-005`) for vector embeddings.
* **Database:** Google Cloud Firestore (utilizing native vector search for semantic retrieval).
* **Infrastructure:** Google Cloud Platform (Cloud Run for the API, Cloud Tasks for debouncing/async work, Cloud Scheduler for crons, Secret Manager).
* **RAG Implementation:** Uses Reciprocal Rank Fusion (RRF) to combine keyword and vector search results before LLM context injection.

## Project Structure

* **`cmd/jot/`**: The CLI client for interacting with the backend API.
* **`cmd/server/` & `cmd/local/**`: Entry points for the Cloud Run deployment and local testing.
* **`internal/prompts/`**: System instructions for the various AI personas and extraction tasks.
* **`internal/tools/`**: The tool registry for the agentic loop (web search, memory upserts, date calculators).
* **`agents.go` & `cron.go**`: Logic for the Dreamer, Specialists, and background consolidation.
* **`query_agent.go`**: The main Front-of-House (FOH) ReAct loop handling user queries.

## Usage Examples (CLI)

JOT uses natural language processing, so you don't always need strict commands.

```bash
# Log a simple entry (Episodic Memory)
$ jot Had coffee with Sarah today, she prefers oat milk.

# Query your semantic memory
$ jot query What does Sarah put in her coffee?

# Generate a structured plan
$ jot plan Rebuild my home network

# Run the nightly distillation manually
$ jot dream

# Open interactive entry editor
$ jot edit 10

```

## Deployment

This project is designed to be deployed to Google Cloud Platform.

1. **Infrastructure:** Run `./setup-infra.sh` to configure Cloud Tasks, Scheduler, and necessary APIs.
2. **Secrets:** Run `./setup-secrets.sh` to load API keys (Gemini, Twilio) into GCP Secret Manager.
3. **Deploy:** Run `./deploy.sh container` to build and push the image to Cloud Run.
4. **Database Indexes:** Firestore composite and vector indexes must be deployed via `firebase deploy --only firestore` using the included `firestore.indexes.json`.

# JOT: Agentic Second Brain

JOT is a single-user, AI-driven personal assistant and knowledge graph built in Go. It operates on the principle of separating **"Gold"** (permanent facts, relationship details, project milestones) from **"Gravel"** (temporary logistics, conversational filler, one-off errands).

It continuously ingests raw, chronological daily logs (Episodic Memory) and uses Google's Gemini LLMs to distill them into a highly structured, vector-searchable Knowledge Graph (Semantic Memory).

## Features

* **Multi-Channel Ingestion:** Log entries via CLI, Twilio SMS, or by typing into a synced Google Doc.
* **Agentic Query System:** Ask questions about your life, projects, or the world. JOT uses an agentic loop with tools for semantic search, Wikipedia, web search, and mathematical/date calculations.
* **The Dreamer (Nightly Consolidation):** A background chron job that reviews the last 24 hours of logs using a "Committee of Minds" (Anthropologist, Architect, Executive, Philosopher, Self-Modeler) to extract facts and update your permanent persona profile.
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
* **`internal/agent/`** & **`internal/service/cron.go`**: Logic for the Dreamer, Specialists, and background consolidation.
* **`internal/agent/foh.go`**: The main Front-of-House (FOH) ReAct loop handling user queries.

## Usage Examples (CLI)

JOT uses natural language processing, so you don't always need strict commands.

```bash
# Log a simple entry (Episodic Memory)
$ jot Had coffee with Sarah today, she prefers oat milk.

# Query your semantic memory
$ jot What does Sarah put in her coffee

# Run the nightly distillation manually
$ jot dream

# Sync with a google doc
$ jot sync

```

## Deployment

This project is designed to be deployed to Google Cloud Platform.

1. **Configure GCP:** When (re)starting the project, run `./scripts/setup-infra.sh` (APIs, Cloud Tasks queue, Scheduler jobs) and `./scripts/setup-secrets.sh` (Secret Manager: Gemini API key, JOT_API_KEY, optional Twilio). Alternatively configure APIs and secrets in the GCP Console or via gcloud.
2. **Deploy:** Run `./scripts/deploy.sh` (or `./scripts/deploy.sh container`) to build, test, push the image to Cloud Run, and deploy Firestore indexes from `firestore.indexes.json`. The deploy uses a Cloud Run service YAML that includes the **Managed Service for Prometheus sidecar**, which scrapes `GET /metrics` (Prometheus exposition format) and sends custom metrics to Google Cloud.
3. **Local testing:** Run `./scripts/test-local.sh` to start the API locally. Tail logs with `./scripts/tail.sh`.

**Viewing custom metrics (Prometheus):** After deploy, custom metrics (e.g. `jot_llm_*`, `jot_embedding_*`, `jot_queries_total`) are scraped by the sidecar and written to [Google Cloud Managed Service for Prometheus](https://cloud.google.com/stackdriver/docs/managed-prometheus). Open [Metrics Explorer](https://console.cloud.google.com/monitoring/metrics-explorer), switch the query language to **PromQL**, and run queries such as `rate(jot_llm_calls_total[5m])` or `jot_embedding_calls_total`.

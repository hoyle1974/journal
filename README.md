# JOT: Agentic Second Brain

JOT is a single-user, AI-driven personal assistant and knowledge graph built in Go. It separates **"Gold"** (permanent facts, relationship details, project milestones) from **"Gravel"** (temporary logistics, conversational filler, one-off errands).

It continuously ingests raw, chronological journal entries (Episodic Memory) and uses Google's Gemini LLMs to distill them into a highly structured, vector-searchable Knowledge Graph (Semantic Memory) via the **Project Loom** pipeline.

## Features

* **Multi-Channel Ingestion:** Log entries via CLI or Telegram (text, images, voice notes).
* **Agentic Query System:** Ask questions about your life, projects, or the world. JOT uses an agentic ReAct loop with tools for semantic search, knowledge graph traversal, task management, web search, and more. Responses include a reasoning trace showing the model's chain of thought.
* **Project Loom Pipeline:** Every ingested entry synchronously runs the Refinery — extracting relationship triples and distilling Gold facts into the knowledge graph, so memory is always up to date.
* **Project Engine:** Tasks with subtasks (parent_id hierarchy). Use `decompose_task` to break complex goals into subtasks; the active project is injected into every query prompt.

## Architecture & Tech Stack

* **Language:** Go 1.23+
* **AI/LLM:** Google Gemini (2.5 Flash) for reasoning and data extraction; Vertex AI (`text-embedding-005`) for vector embeddings.
* **Database:** Google Cloud Firestore (native vector search for semantic retrieval). All documents — episodic logs and semantic knowledge nodes — live in the `journal` collection, distinguished by `node_type`.
* **Infrastructure:** Google Cloud Platform (Cloud Run for the API, Cloud Tasks for async work, Cloud Scheduler for crons, Secret Manager for secrets).
* **RAG:** Reciprocal Rank Fusion (RRF) combines keyword and vector search results before LLM context injection.

## Project Structure

* **`cmd/jot/`**: CLI client (`log`, `query`, `edit`, `entries`).
* **`cmd/server/`**: Cloud Run API entry point.
* **`internal/prompts/`**: System instructions for the various AI agents and extraction tasks.
* **`internal/tools/`**: Tool registry for the agentic loop (semantic search, journal, task, web, utility tools).
* **`internal/agent/foh.go`**: Front-of-House (FOH) ReAct loop handling user queries.
* **`internal/agent/`**: Project Loom pipeline — Refinery extraction, task workers, decay cron, and RAG stages.
* **`internal/service/`**: Service layer connecting agents to Firestore and LLM clients.

## Usage Examples (CLI)

```bash
# Log an entry (Episodic Memory — triggers Loom pipeline)
$ jot Had coffee with Sarah today, she prefers oat milk.

# Query your semantic memory (returns reasoning trace + answer)
$ jot What does Sarah put in her coffee
```

## Deployment

This project is designed to be deployed to Google Cloud Platform.

1. **Configure GCP:** Run `./scripts/setup-infra.sh` (enables APIs, creates Cloud Tasks queue and Scheduler jobs) and `./scripts/setup-secrets.sh` (sets Secret Manager entries: Gemini API key, JOT_API_KEY, Telegram bot token).
2. **Deploy:** Run `./scripts/deploy.sh` to build, push the container image to Cloud Run, and deploy Firestore indexes from `firestore.indexes.json`.
3. **Local testing:** Run `./scripts/test-local.sh` to start the API locally. Tail logs with `./scripts/tail.sh`.

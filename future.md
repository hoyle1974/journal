Features I want to see in the future

* Embedding Token Limits: You are using Vertex AI text-embedding-005. This model has a hard limit of 2048 input tokens. If you dump freeform notes longer than this, the embeddings will silently truncate, causing you to lose semantic search capabilities on the latter halves of long entries.

* Task State Saturation: Tasks are auto-created, and the system injects open root tasks into the prompt. If tasks are not ruthlessly groomed, the agent will lose focus on immediate blockers due to task saturation.


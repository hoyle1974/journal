# Firestore Vector Index for Entry Embeddings

Journal entries are embedded with text-embedding-005 (768 dimensions) for semantic search. A Firestore vector index is required before `QuerySimilarEntries` and `semantic_search` will work.

## Create the index

### Option 1: Google Cloud Console

1. Go to [Firestore Databases](https://console.cloud.google.com/firestore/databases)
2. Click **Create Index** → **Create vector index**
3. Collection ID: `entries`
4. Vector field: `embedding`, dimension: `768`
5. Save

### Option 2: gcloud

```bash
gcloud firestore indexes composite create \
  --collection-group=entries \
  --query-scope=COLLECTION \
  --field-config=field-path=embedding,vector-config='{"dimension":"768", "flat": "{}"}' \
  --database="(default)"
```

### Option 3: Firebase CLI

If you have Firebase configured:

```bash
firebase deploy --only firestore
```

Uses `firestore.indexes.json` in this directory.

## Backfill existing entries

New entries get embeddings automatically (async). For existing entries, run:

```bash
./backfill-embeddings.sh
```

Uses `JOT_API_URL` and `JOT_API_KEY` from `.env`. Loops until done (rate limited 2/min). Set `BACKFILL_EMBEDDING_LIMIT` in .env to control batch size (default 20).

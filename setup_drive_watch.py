#!/usr/bin/env python3
"""
Set up Google Drive Watch to receive notifications when the journal doc changes.

This needs to be run periodically (e.g., weekly) to renew the watch subscription,
as Drive watch channels expire.

Usage:
    python setup_drive_watch.py

Requires:
    - DOCUMENT_ID in .env
    - SERVICE_ACCOUNT_FILE in .env (or use gcloud auth)
    - GDOC_WEBHOOK_URL set after deployment
"""
import os
import time
import uuid

# Load .env if present (optional: use python-dotenv if installed, else read .env manually)
def _load_dotenv():
    path = os.path.join(os.path.dirname(__file__) or '.', '.env')
    if not os.path.isfile(path):
        return
    try:
        from dotenv import load_dotenv
        load_dotenv(path)
    except ImportError:
        with open(path) as f:
            for line in f:
                line = line.strip()
                if line and not line.startswith('#') and '=' in line:
                    k, _, v = line.partition('=')
                    os.environ.setdefault(k.strip(), v.strip().strip('"').strip("'"))

_load_dotenv()

DOCUMENT_ID = os.environ.get('DOCUMENT_ID')
SERVICE_ACCOUNT_FILE = os.environ.get('SERVICE_ACCOUNT_FILE', 'credentials.json')
GOOGLE_CLOUD_PROJECT = os.environ.get('GOOGLE_CLOUD_PROJECT', '')
REGION = os.environ.get('CLOUD_TASKS_LOCATION', 'us-central1')

# Webhook URL - update after deployment
GDOC_WEBHOOK_URL = os.environ.get(
    'GDOC_WEBHOOK_URL',
    f'https://{REGION}-{GOOGLE_CLOUD_PROJECT}.cloudfunctions.net/jot-api-go/webhook'
)


def setup_watch():
    """Set up Drive watch for the journal document."""
    if not DOCUMENT_ID:
        print("Error: DOCUMENT_ID not set in .env")
        return False

    try:
        from google.oauth2 import service_account
        from googleapiclient.discovery import build

        # Get credentials
        creds = service_account.Credentials.from_service_account_file(
            SERVICE_ACCOUNT_FILE,
            scopes=['https://www.googleapis.com/auth/drive']
        )

        # Build Drive service
        drive_service = build('drive', 'v3', credentials=creds)

        # Channel ID - unique identifier for this watch subscription
        channel_id = f'jot-sync-{uuid.uuid4().hex[:8]}'

        # Expiration: 7 days from now (max allowed by Drive API)
        expiration = int((time.time() + 604800) * 1000)  # milliseconds

        # Set up the watch
        watch_response = drive_service.files().watch(
            fileId=DOCUMENT_ID,
            body={
                'id': channel_id,
                'type': 'web_hook',
                'address': GDOC_WEBHOOK_URL,
                'expiration': expiration
            }
        ).execute()

        print("Drive Watch setup successful!")
        print(f"  Channel ID: {channel_id}")
        print(f"  Resource ID: {watch_response.get('resourceId')}")
        print(f"  Expiration: {watch_response.get('expiration')}")
        print(f"  Webhook URL: {GDOC_WEBHOOK_URL}")
        print()
        print("The watch will expire in 7 days. Run this script again to renew.")
        print()
        print("To stop the watch:")
        print(f"  Channel ID: {channel_id}")
        print(f"  Resource ID: {watch_response.get('resourceId')}")

        # Save channel info for potential future stop
        with open('.drive_watch_channel', 'w') as f:
            f.write(f"channel_id={channel_id}\n")
            f.write(f"resource_id={watch_response.get('resourceId')}\n")

        return True

    except Exception as e:
        print(f"Error setting up Drive watch: {e}")
        return False


def stop_watch():
    """Stop an existing Drive watch."""
    try:
        # Read channel info
        if not os.path.exists('.drive_watch_channel'):
            print("No watch channel info found. Nothing to stop.")
            return

        with open('.drive_watch_channel', 'r') as f:
            lines = f.readlines()

        channel_id = None
        resource_id = None
        for line in lines:
            if line.startswith('channel_id='):
                channel_id = line.split('=')[1].strip()
            if line.startswith('resource_id='):
                resource_id = line.split('=')[1].strip()

        if not channel_id or not resource_id:
            print("Invalid channel info. Cannot stop watch.")
            return

        from google.oauth2 import service_account
        from googleapiclient.discovery import build

        creds = service_account.Credentials.from_service_account_file(
            SERVICE_ACCOUNT_FILE,
            scopes=['https://www.googleapis.com/auth/drive']
        )

        drive_service = build('drive', 'v3', credentials=creds)

        drive_service.channels().stop(body={
            'id': channel_id,
            'resourceId': resource_id
        }).execute()

        print(f"Watch stopped: {channel_id}")
        os.remove('.drive_watch_channel')

    except Exception as e:
        from googleapiclient.errors import HttpError
        if isinstance(e, HttpError) and e.resp.status == 404:
            print("Previous watch already expired or stopped.")
            if os.path.exists('.drive_watch_channel'):
                os.remove('.drive_watch_channel')
        else:
            print(f"Error stopping watch: {e}")


if __name__ == '__main__':
    import sys

    if len(sys.argv) > 1 and sys.argv[1] == 'stop':
        stop_watch()
    else:
        sys.exit(0 if setup_watch() else 1)

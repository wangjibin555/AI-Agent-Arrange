#!/usr/bin/env python3
"""
SSE Client Example for AI Agent Arrange
Real-time task monitoring using Server-Sent Events
"""

import requests
import json
import sseclient
from datetime import datetime

API_BASE = 'http://localhost:8080/api/v1'


def create_task(action, parameters):
    """Create a new task"""
    url = f'{API_BASE}/tasks'
    data = {
        'action': action,
        'parameters': parameters
    }

    print(f"🚀 Creating task: action={action}")
    response = requests.post(url, json=data)
    result = response.json()

    if result.get('success'):
        task_id = result['data']['id']
        print(f"✅ Task created: {task_id}\n")
        return task_id
    else:
        print(f"❌ Failed to create task: {result.get('error')}")
        return None


def monitor_task(task_id):
    """Monitor task events via SSE"""
    url = f'{API_BASE}/tasks/{task_id}/stream'

    print(f"📡 Connecting to SSE stream: {url}")
    print(f"{'='*60}\n")

    try:
        response = requests.get(url, stream=True, timeout=60)
        client = sseclient.SSEClient(response)

        for event in client.events():
            timestamp = datetime.now().strftime('%H:%M:%S')

            # Parse event data
            try:
                data = json.loads(event.data)
                event_type = event.event or 'message'

                # Print event
                print(f"[{timestamp}] 📨 Event: {event_type}")
                print(f"  Status: {data.get('status', 'N/A')}")
                print(f"  Message: {data.get('message', 'N/A')}")

                # Print result for completed events
                if event_type == 'completed' and data.get('result'):
                    print(f"  Result: {json.dumps(data['result'], indent=2)}")

                # Print error for failed events
                if event_type == 'failed' and data.get('error'):
                    print(f"  Error: {data['error']}")

                # Print token for token_generated events
                if event_type == 'token_generated' and data.get('result'):
                    token = data['result'].get('token', '')
                    full_text = data['result'].get('text', '')
                    print(f"  Token: {token}")
                    print(f"  Full Text: {full_text[:50]}..." if len(full_text) > 50 else f"  Full Text: {full_text}")

                print()

                # Close connection on terminal events
                if event_type in ['completed', 'failed', 'cancelled']:
                    print(f"✅ Task {event_type}. Closing connection.\n")
                    break

            except json.JSONDecodeError:
                print(f"⚠️  Invalid JSON in event data: {event.data}")

    except requests.exceptions.Timeout:
        print("⏱️  Connection timeout")
    except KeyboardInterrupt:
        print("\n⚠️  Interrupted by user")
    except Exception as e:
        print(f"❌ Error: {e}")


def cancel_task(task_id):
    """Cancel a task"""
    url = f'{API_BASE}/tasks/{task_id}'

    print(f"🚫 Cancelling task: {task_id}")
    response = requests.delete(url)
    result = response.json()

    if result.get('success'):
        print(f"✅ Task cancelled successfully\n")
        return True
    else:
        print(f"❌ Failed to cancel task: {result.get('error')}\n")
        return False


def main():
    print("=" * 60)
    print("🤖 AI Agent Arrange - SSE Client Example")
    print("=" * 60)
    print()

    # Example 1: Echo task
    print("📝 Example 1: Simple Echo Task")
    print("-" * 60)
    task_id = create_task('echo', {'message': 'Hello from Python!'})
    if task_id:
        monitor_task(task_id)

    # Example 2: Translation task (if available)
    print("\n📝 Example 2: Task with Custom Parameters")
    print("-" * 60)
    task_id = create_task('echo', {
        'message': 'Real-time monitoring is awesome!',
        'delay_ms': 2000
    })
    if task_id:
        monitor_task(task_id)

    print("\n✅ Examples completed!")


if __name__ == '__main__':
    try:
        main()
    except KeyboardInterrupt:
        print("\n\n👋 Goodbye!")
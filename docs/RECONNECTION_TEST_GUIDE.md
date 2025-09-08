# Network Reconnection Test Guide

## Feature Overview

We have implemented a robust network reconnection mechanism that automatically attempts to restore the session when the connection to Speechmatics is unexpectedly lost.

## Implementation Details

1. **Connection State Monitoring**: The app now monitors the WebSocket `socketState` changes
2. **Automatic Reconnection**: When a disconnection is detected (`closing` or `undefined` state), the app will:
   - Display "Reconnecting..." status
   - Obtain a new JWT token
   - Attempt to re-establish connection using the original configuration
3. **UI Feedback**: 
   - Microphone indicator shows orange "Reconnecting..." status
   - Error message area displays yellow warning style

## Testing Steps

### 1. Simulate Network Interruption

1. Start the application and begin transcription
2. Use one of the following methods to simulate network interruption:
   - **Method A**: Disconnect network connection (unplug cable or turn off WiFi)
   - **Method B**: Use browser developer tools:
     - Open DevTools (F12)
     - Go to Network tab
     - Select "Offline" mode
   - **Method C**: Use firewall to temporarily block connection to Speechmatics

3. Observe application behavior:
   - Status should change to "Reconnecting..."
   - Error message displays "Connection lost. Attempting to reconnect..."

### 2. Restore Network Connection

1. Restore network connection
2. The application should automatically:
   - Obtain a new JWT token
   - Re-establish WebSocket connection
   - Resume normal "Recording" status
   - Clear error message

### 3. Verify Functionality

- Confirm transcription works normally after reconnection
- Check that previous transcription content is preserved
- Verify new voice input is correctly transcribed

## Important Notes

- If reconnection fails, it will display "Failed to reconnect. Please stop and restart transcription."
- In some cases, you may need to manually stop and restart transcription
- During reconnection, audio recording continues but transcription is temporarily interrupted

## Log Monitoring

In the browser console, you should see the following logs:
- `WebSocket disconnected, attempting to reconnect...`
- `Successfully reconnected to Speechmatics` (on success)
- `Failed to reconnect: [error details]` (on failure)
# Frontend Conventions (Preact/HTM)

## Stack
- **UI Framework**: Preact with HTM (tagged template literals, no JSX/build step)
- **Styling**: Tailwind CSS
- **State**: React-style hooks (useState, useCallback, useRef, useMemo)
- **WebSocket**: Custom `useWebSocket` hook in `web/static/hooks/useWebSocket.js`

## Component Template
```javascript
import { html } from "../lib.js";
import { useState, useCallback } from "preact/hooks";

export function MyComponent({ propA, propB = false }) {
  const [state, setState] = useState(null);
  return html`<div class="my-class">${state}</div>`;
}
```

## Key Components
| Component | File | Purpose |
|-----------|------|---------|
| `ChatInput` | `components/ChatInput.js` | Message composition, image/file upload, slash commands |
| `Message` | `components/Message.js` | Message rendering (agent, user, tool calls) |
| `SettingsDialog` | `components/SettingsDialog.js` | Application settings UI |
| `app.js` | `app.js` | Main application, state management, WebSocket integration |

## Data Flow: Backend → Frontend
1. Backend sends data in WebSocket `connected` message payload
2. `useWebSocket.js` stores in session info via `setSessions()`
3. `app.js` destructures from hook: `const { sessionInfo } = useWebSocket()`
4. Props passed to components: `agentSupportsImages=${sessionInfo?.agent_supports_images ?? false}`

## Adding New Session Capabilities to Frontend
1. **useWebSocket.js** — In `case "connected":` handler, add to session.info:
   ```javascript
   my_new_capability: msg.data.my_new_capability ?? false,
   ```
2. **app.js** — Pass as prop to the relevant component:
   ```javascript
   myNewCapability=${sessionInfo?.my_new_capability ?? false}
   ```
3. **Component** — Accept prop with default, use for conditional rendering:
   ```javascript
   export function ChatInput({ myNewCapability = false, ...rest }) {
     // Disable UI or show warning based on capability
   }
   ```

## Image Upload Flow (ChatInput)
- `pendingImages` state: array of `{ id, url, name, mimeType, uploading }`
- Upload: `uploadImage(file)` → POST `/api/sessions/{id}/images` → update state
- Send: `onSend({ message, image_ids: readyImages.map(i => i.id) })`
- Three entry points: paste handler, drag-drop handler, file picker button
- All three should check `agentSupportsImages` before processing

## Anti-Patterns
- **Don't use `useMemo` for context menu positions** — use `useState` (see augment rule 20)
- **Don't skip `??` nullish coalescing** — WebSocket data may be undefined, always provide defaults
- **Don't forget `LoadEvents`** — WebSocket clients must send `load_events` after connecting to receive streaming events

# Spec: UI Push-to-Talk (PTT)

Push-to-talk microphone contract and voice-first input mode. Shared across all UI components.

## Push-to-Talk (PTT)

The microphone button in the input bar:
- **Hold**: starts recording via Web Speech API
- **Release**: stops recording, transcribed text is sent immediately (not placed in field)
- **Visual state**: button turns red with pulse animation while recording
- **Fallback**: if Speech API unavailable or on HTTP, button is dimmed (40% opacity); tapping shows alert explaining why

### Voice-first mode

Configurable in **Settings → Input → Default input method** (stored in `localStorage` as `mclaude.inputMode: 'text' | 'voice'`; default `'text'`).

**Text mode** (default): PTT button is small (32px) and sits between Attach and Send. Layout: `[Stop] [📷] [textarea] [🎙] [Send]`.

**Voice mode**: the Send button is replaced by a large microphone button (56×56px). The textarea shrinks but remains visible (users can still type and press Enter to send). Layout: `[Stop] [📷] [textarea…] [large 🎙]`.

In voice mode the large button uses the same hold-to-record / release-to-send semantics. A keyboard icon (⌨) appears in the top-right corner of the input area; tapping it temporarily focuses the textarea and collapses the button back to small size until focus is lost.

**Settings screen** adds an "Input" section:
```
Input
  Default method:  ○ Text  ● Voice
```
The preference is persisted in `localStorage` under `mclaude.inputMode` and applied on every render of the input bar.

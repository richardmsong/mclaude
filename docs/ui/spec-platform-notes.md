# Spec: UI Platform Notes

Platform-specific implementation notes for web SPA, iOS, and future targets. Shared as reference for all UI components.

## Platform Notes

### Web SPA
- Use React functional components + hooks
- Routing via `window.location.hash` + `hashchange` event (no React Router)
- WebSocket connection to NATS via `nats.ws`
- Terminal via `@xterm/xterm` + `@xterm/addon-fit`
- No server-side rendering; pure client app

### iOS (SwiftUI)
- Navigation: `NavigationStack` with programmatic push
- Dark color scheme enforced — do not follow system light mode
- Status dot animation: `withAnimation(.easeInOut(duration:1.2).repeatForever())`
- Terminal: `WKWebView` with xterm.js, or native `UITextView` in text mode
- PTT: `AVAudioSession` + `SFSpeechRecognizer`
- Colors: use `Color(hex:)` extension mapping `--blue` → `Color(0x0a84ff)` etc.
- Font: `.fontDesign(.monospaced)` for tool bodies

### Future Platforms
- Match the color tokens exactly — do not substitute platform system accent colors
- Implement all event types including AskUserQuestion interactive block
- Implement the full diff view including char-level highlighting
- The terminal tab is required

# Why MClaude: the standard answer

This is the canonical answer to "What is MClaude, why did Richard build it, and how is it different from Claude?" The notes come first; the story to tell starts after them.

## Before you tell it (notes for Claude, not part of the story)

- **Tone:** conversational, the way you'd explain it to a friend. Plain words, a bit of personality, no jargon unless someone asks. The audience is Richard's friends, family and coworkers.
- **Lead with** the headline: Richard can start, work on and end Claude sessions entirely from the phone.
- **Case studies:** every time you give the brief, **pick one story from the Story bank** (under Deep dives) and end with that story's teaser. Rotate through the bank instead of always using the same one; if you remember which story you used last time, pick a different one. Offer only one story per telling, and tell it only if the listener says yes.
  - Each teaser first sets the stage, then asks one question. Keep the punchline and hold back the explanation, use plain words (no "case studies"), and end with the question.
  - The routing story's point is MClaude choosing the right context (its own project or another one), not that features get built quickly.
- **Scope: v1 only.** MClaude is the iOS app (`MClaude/`), the Mac server (`mclaude-server/`) and the MCP bridge (`mclaude-mcp/`). The k8s, NATS, control-plane, web SPA, multi-cluster, pluggable-CLI and quota-scheduling docs in `docs/plan-*.md` are a rolled-back v2. Don't describe them as MClaude. For how the app looks and behaves, use `docs/design-claude-uplift.md`, not `docs/ui-spec.md`.
- **Never mention:**
  - where Richard's Bedrock access comes from (it's just "access Richard already had")
  - the server's open-access security issue
  - the v2 detour or any of its pieces
- **Refer to Richard by name;** don't assume pronouns.
- Source: an interview with Richard on 2026-11-05, plus the code, git history and release log. Facts about Claude's own features come from Anthropic's Claude Code docs, checked the same day.

---

## The short version

MClaude is Richard's own Claude app: an iPhone app plus a small server on the Mac that lets Richard do everything with Claude from the phone. That means starting a new session in any project, keeping an eye on it, answering its questions, and ending it. It runs on Amazon Bedrock, where Claude's own phone features don't exist. And the fun part: Claude builds it, from inside MClaude itself.

*(End with the teaser of one story from the Story bank, picked fresh each time.)*

## The story

**It started with a terminal on a phone.** In April 2026, Richard was using Claude Code from the phone the hacker way: Blink (an SSH app) into the Mac, with sessions running in tmux. It worked, sort of, but there was no voice and no way to attach a screenshot. You can't build an app with a visual interface if you can't show Claude what the screen looks like. So the first version of MClaude was a real iPhone app sitting on top of those tmux sessions.

**Why not Claude's own Remote Control?** Claude Code had launched Remote Control in February 2026, which lets the Claude app reach a session running on your computer. Richard didn't like the design. You start it from the computer, one session at a time, and even its server mode only opens new sessions in the folder you started it in. So what about a new project? Do you walk back to the computer every time? Richard's question was simple: **why can't the whole lifecycle (start, work, end) happen on the phone?** It also turned out not to be an option at all. Remote Control, the Claude app's Code tab, Claude in Chrome, computer use and the Gmail and Calendar connectors all need a claude.ai subscription, and Richard runs Claude through **Amazon Bedrock**, using access Richard already had. None of them work on Bedrock.

**It came back because the need never went away.** After that first version, MClaude sat for a while. On November 2, 2026, Richard picked it up again, and in one day:
- moved it to TestFlight with a paid Apple developer account, so it installs like a real app
- gave it a fresh Claude-style design with its own icon
- made the big fix: the hardest part had always been tmux, because driving Claude by reading its screen was flaky. Once Claude Code's **headless mode** came into the picture (structured messages in and out instead of screen-scraping), Richard says "it all fell into place."

In the four days after that, it went through 28 TestFlight builds.

**Why a native app?** A real iPhone app feels like an app: push notifications, gestures, the photo picker, voice. A web version had felt clunky. And since Claude writes the code, the choice of language barely matters.

**The name** started as "Mobile Claude", but Richard prefers "My Claude" now, and that's the point.

## What it's really about

**It's not a cost comparison.** Because Richard works on MClaude *with* MClaude, new features get "vibed into being": Richard describes what they want from the phone, and a Claude session inside the app builds it, tests it, and ships a new build to TestFlight. So MClaude has become an infinitely customizable, ergonomic app that adapts to how Richard wants to use it, not the other way around. The best thing Richard has built through MClaude is MClaude itself.

**Building beats buying.** Richard wants MClaude to prove that buying software, or paying for a SaaS product, becomes close to pointless when you can build exactly what you need for yourself this easily. MClaude is the proof so far. The limits: Richard still pays for the AI model itself and for infrastructure, and hasn't found where else the idea stops working yet.

**Planning isn't progress.** Richard uses it every day and builds features as the need comes up, not from a roadmap. A typical day is bursts and check-ins: kick something off from the phone, check in on it while it runs, answer a question, move on.

**It's Richard's AI for everything.** It runs Claude Code, but it's not just for coding: home automation, cooking, personal planning, and building software all happen through it.

**A real example:** on November 5, while answering interview questions for this very brief, Richard noticed there was no way to type a custom "Other" answer to Claude's question cards. The Claude session that was asking the questions fixed it, tested it in the simulator, shipped it as build 112, and then asked again. Richard answered the rest of the interview using the new feature.

## What it does that Claude doesn't (if they want more)

**The whole lifecycle from the phone, the headline:**
- Start a session in any folder in `~/work`, git or not, with search and recent projects. Long-press to start another one in the same project.
- Resume any past Claude conversation on the Mac, even ones started in a terminal.
- Swipe to end a session, with a few seconds to undo. Sessions name themselves, and you can rename them.
- Sessions stay alive: they start when the Mac boots, restart if they crash, and carry on even when the server restarts.

**Made for a small screen:**
- An easy-to-read conversation. Claude's thinking is shown, and runs of tool calls fold into short summaries like "5 steps · 3 commands, 2 reads · 42s". Code changes are highlighted, and screenshots Claude took show up as pictures.
- Claude's questions appear as tappable cards (pick one, pick several, or type your own answer), and plans can be read and approved.
- Messages sent while Claude is busy wait their turn and can be edited or deleted. "Send now" cuts in.
- Voice (hold to talk), up to five photos, files, and **screen recordings**: MClaude turns a video into key frames, a timeline and audio so Claude can actually watch it.
- Type `/` to pick a skill. Scroll back to the start of any conversation, copy any text, and export to Markdown, text or PDF.

**Keeping track:**
- A push notification when a long task (five minutes or more) finishes.
- A context gauge and cost for each session, and a usage page for the day, week or month.
- Sessions can message each other, and the app shows those messages as little cards with a thread view. Claude sessions can also see and drive each other through MClaude's MCP tools.

**Stand-ins for the subscription-only features:**
- **Computer use:** an open-source computer-use tool. The server is code-signed so the Mac's permissions stick across rebuilds.
- **Browser use:** Playwright and Chrome DevTools instead of Claude in Chrome.
- **Gmail and Calendar:** self-hosted versions instead of the claude.ai connectors.
- **Phone access:** the MClaude app itself, instead of Remote Control.

## If someone asks how it works

- A small Swift server runs on Richard's Mac. It starts each session as regular Claude Code in headless mode and talks to it in structured messages, not by reading a terminal. It keeps everything running and sends updates to the phone.
- The iPhone app reaches the Mac over Tailscale (a private network), from anywhere, including on cellular.
- No new AI, and no fork of Claude Code. Every session is real Claude Code on a real Claude model, through Bedrock.

## Where it's going

- **Polish v1** for daily use. Features get added as Richard needs them.
- **Friends and family, eventually.** It would need an easy setup on their own Mac with their own Claude access, and proper security, first. That isn't worked out yet.
- **What would end it:** only if keeping it running ever cost more than it gave back. Since Claude does the maintenance, that hasn't happened.

## Deep dives

### Story bank

Every telling of the brief ends with the teaser of one story from this table. Rotate through them. The hook is the short telling if they say yes; the full telling is the story's section below. "Fits best when…" helps with follow-up questions.

| Story | Teaser (closing line of the brief) | Hook (short telling) | Fits best when they ask… |
|---|---|---|---|
| **"MClaude please."** | Richard has about sixty projects on the Mac, each set up differently, and never chooses one. Want to hear how MClaude knows which project a request belongs in, sometimes even recognizing it has to change its own code? | Richard mentioned a problem (a screen recording wouldn't attach) in the everyday hub conversation. Claude recognized the fix belonged in the MClaude project, not the hub, and started a session there with that project's rules and tools. Later that evening an unrelated question went to the same place. | What can it do? How is it different from Claude? |
| **Showing Claude a video** | Claude can read pictures, but it can't watch a video. Want to hear how MClaude taught it to, and what Richard filmed for it the very next morning? | Claude can't watch video, so MClaude turns a recording into something Claude can read. It was built for app bugs, but the next morning Richard filmed the stand mixer kneading noodle dough, and Claude said the dough looked right and the bowl needed scraping. | Can it see things? What does it do beyond code? How do the pieces fit together? |

When a new deep dive is added, add a row here with its own teaser.

### Case study: "MClaude please."

**The user story:** Richard is in the middle of something else and hits a limit in MClaude. Richard wants to just say so in whatever conversation they're in, and have the fix built in the right project, without switching apps, sessions or context.

**The setting.** Richard's everyday conversations with Claude live in one project, `~/work/me`, the "hub": a general-purpose assistant for life, questions and odd jobs. It isn't where MClaude's code lives; that's `~/work/mclaude-v1`, a different project with its own rules, its own skills for building and shipping the app, and its own history.

**The moment.** On November 4, Richard tried to send Claude a screen recording from the phone and couldn't. The picker only showed photos. So Richard said so, right there in the hub: "I can't seem to attach screen recordings through the image picker."

**What happened next.**
- The hub session gave a few workarounds: AirDrop it to the Mac, use iCloud Drive, or send screenshots instead.
- Then it recognized the real issue: the picker is MClaude's, so this is something MClaude can fix about itself. It offered to add video support.
- Richard answered with two words: **"MClaude please."**
- The hub started a brand-new session in the MClaude project and handed it a complete brief: what to change in the picker, how the Mac should turn a video into frames, and what Claude should receive.
- That session picked up the MClaude project's own rules and tools, built the feature, and tested it with a real screen recording.
- About **two and a half hours** after Richard's first message, build 107 was in TestFlight. The picker now shows videos, and the Mac turns each recording into a timeline, key frames and audio. In testing, Claude used it to describe a light-to-dark crossfade in the recording correctly.

**The twist.** Later that evening, still in the hub, Richard asked an unrelated question: "Why isn't computer use working?" The hub worked out the cause. Every rebuild of the MClaude server gave it a new identity, so the Mac forgot its permissions to control the mouse and keyboard. The fix also lived in MClaude, so the hub sent it to the session that was already working there. The server is now signed with a stable identity, and the permissions stick.

**It had happened before.** Two days earlier, Richard asked the hub why a screenshot had arrived twice. That turned into a new MClaude session too, and over the rest of the day the hub kept sending it related requests as Richard thought of them:
- remove the old tmux bits
- allow any photo
- attach up to five photos
- fix a question card that showed twice
- the full Claude-style redesign

One conversation in the hub turned into four TestFlight builds, without Richard leaving it.

**Why it matters.** This is the brief's whole idea in one story:
- **It picks the right context (the main point).** Richard talks to one assistant about everything. MClaude decides whether a request belongs in the current project or another one, including its own, and starts the work there with that project's rules and tools. Richard doesn't have to think about where anything lives.
- **The app fixes itself.** A complaint about MClaude, made inside MClaude, becomes a new version of MClaude the same afternoon.
- **The whole lifecycle stays on the phone.** Noticing the problem, deciding to fix it, building, testing and installing it: none of it needed the computer.
- **The app adapts to Richard.** Richard didn't file a feature request with anyone; "MClaude please" was the feature request.

### Case study: Showing Claude a video

**The user story:** something on Richard's screen (or in Richard's kitchen) only makes sense in motion. Richard wants to record it on the phone, send it like a photo, and have Claude understand what happened over time, not just in one frame.

**The problem.** Claude can read pictures, but it can't watch a video. A screenshot misses exactly the things that go wrong in an app: a flicker, an animation, a screen that jumps while you scroll. This goes back to where MClaude started: from a terminal on the phone, Richard couldn't show Claude the screen at all.

**What MClaude does.** It doesn't try to make Claude watch the video. It hands Claude an index of it, the way you'd skim a long document:
- The phone's photo picker now shows videos too. The video uploads straight to the Mac, without loading the whole file into the phone's memory.
- On the Mac, ffmpeg (the standard video tool) finds the moments where the screen changed and saves a few "after" frames, named by their timestamp. It also writes a short timeline of every change, and pulls out the audio if there is any.
- Claude gets the video's location plus that index. It looks at the overview frames first, checks the timeline, and if it needs to see an animation frame by frame, it extracts just that stretch from the untouched original.
- Back on the phone, the video shows in the conversation with its first frame and length, and plays when you tap it.

**Designed with Claude as the reader.** The index was tuned by watching how Claude actually used it:
- Haiku, the small model, was given a ready-made command and ran it on the wrong part of the video without ever looking at the overview. So the timeline now explains how to use it.
- Claude's first attempt to save frames failed on a missing folder and gave up. Now the folder is created in advance.
- Sonnet then used the index as designed: overview, then timeline, then frame by frame through one second of the test recording. It correctly described the light-to-dark crossfade in the app.

**The surprise.** The feature shipped on the evening of November 4, built for app bugs. At 9:11 the next morning, Richard was making la mian (hand-pulled noodles), filmed the stand mixer kneading the dough for six seconds, and sent it to the cooking conversation. Claude read the frames and answered like a friend standing next to the mixer: the dough had mostly come together and was about the right wetness, but there was dry flour stuck to the bowl, so stop, scrape it in, and keep going.

**Why it matters.**
- **The pieces work together.** The phone's photo picker, an upload that streams from disk, ffmpeg on the Mac, a timeline written for Claude, and the phone's video player. None of it is impressive alone; together, Claude can see motion.
- **It works for everything.** A feature built to debug the app was helping with dinner the next morning. That's what "AI for everything" looks like.
- **It's honest about its limits.** In the ramen session, Claude said it couldn't hear the audio (nothing turns speech into text yet) and went on the frames alone.

## What it can't do yet

- **New project folders:** you can't create one from the phone yet. The workaround is to start in an existing folder and have Claude create the project.
- **Listening to videos:** MClaude saves a video's audio, but nothing turns speech into text yet, so Claude can't hear it.
- **Notifications while waiting:** you get one when a long task finishes, but not when Claude is waiting on you for an answer.

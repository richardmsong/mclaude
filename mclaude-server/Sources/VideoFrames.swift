import Foundation
import AVFoundation

/// Turns an uploaded video (screen recording) into still frames Claude can Read
/// (docs/design-claude-uplift.md → Video attachments).
///
/// 1. ffprobe: duration and whether there's an audio stream.
/// 2. Analysis pass: one decode at a 640px long edge with `select='gte(scene,0)'` +
///    `metadata=print`, giving every frame's index, time and scene score.
/// 3. `selectFrames` (pure): scene-change bursts → their settled last frame, first + last frame,
///    a fallback minimum rate for static stretches, capped at 40.
/// 4. Extraction pass by frame index → JPEGs (long edge ≤ 1600) named by timestamp, a poster,
///    and the audio as a 16 kHz mono wav unless the track is silent.
enum VideoFrames {
    static let ffmpeg = "/usr/local/bin/ffmpeg"
    static let ffprobe = "/usr/local/bin/ffprobe"
    static let maxFrames = 40
    static let sceneThreshold = 0.08
    static let burstGap = 0.5
    /// How long after a burst's last change its settled frame is taken (iOS animations ≈ 0.3–0.5 s).
    static let settle = 0.7
    static let analysisEdge = 640
    static let frameEdge = 1600
    static let posterEdge = 480
    /// Audio whose max volume is at or below this is treated as a silent track.
    static let silentMaxVolumeDB = -80.0
    /// Each ffmpeg run is killed after this long.
    static let processTimeout: TimeInterval = 900

    struct Result: Sendable {
        let videoPath: String
        let duration: Double
        let frameDir: String
        let framePaths: [String]
        let posterPath: String
        let audioPath: String?
        /// Bursts in the motion timeline.
        let changeCount: Int

        /// The note sent to Claude after the video path.
        var annotation: String {
            VideoFrames.annotation(duration: duration, frameCount: framePaths.count, frameDir: frameDir, audioPath: audioPath)
        }
        /// "<video path> [video: …]": this video's fragment of the input message.
        var input: String { "\(videoPath) \(annotation)" }
    }

    struct Failure: Error, CustomStringConvertible {
        let description: String
    }

    typealias Frame = (index: Int, time: Double, score: Double)

    // MARK: - Pure helpers

    static let timelineName = "changes.txt"

    /// `[video: 12.4s, 14 overview frames in /dir/ named by timestamp, motion timeline /dir/changes.txt,
    /// audio: /dir/audio.wav; the original is untouched, extract any stretch at full frame rate with ffmpeg]`.
    /// Never contains "]" (the app and titler match the note with `\[video:[^\]]*\]`).
    static func annotation(duration: Double, frameCount: Int, frameDir: String, audioPath: String?) -> String {
        let dir = frameDir.hasSuffix("/") ? frameDir : frameDir + "/"
        var note = "[video: \(String(format: "%.1f", duration))s, \(frameCount) overview \(frameCount == 1 ? "frame" : "frames") in \(dir) named by timestamp, motion timeline \(dir)\(timelineName)"
        if let audioPath { note += ", audio: \(audioPath)" }
        return note + "; the original is untouched, extract any stretch at full frame rate with ffmpeg (command in \(timelineName))]"
    }

    /// Extracts every real frame of [from, to] into `<dir>/dense/`, named by absolute time in ms.
    static func denseCommand(videoPath: String, frameDir: String, from: Double, to: Double) -> String {
        let dir = frameDir.hasSuffix("/") ? String(frameDir.dropLast()) : frameDir
        return "mkdir -p \(dir)/dense && ffmpeg -v error -ss \(String(format: "%.1f", from)) -to \(String(format: "%.1f", to)) -copyts "
            + "-i \(videoPath) -fps_mode passthrough -frame_pts 1 -enc_time_base 1:1000 -vf \"\(scaleFilter(frameEdge))\" "
            + "\(dir)/dense/t%06dms.jpg"
    }

    /// The motion timeline (`changes.txt`): every burst at the base threshold, with the overview
    /// frame that represents it (`-` if the cap dropped it). `kept` = (time, file name) of the
    /// overview frames, times relative to the video start.
    static func timeline(videoPath: String, frameDir: String, duration: Double, decodedFrames: Int,
                         bursts: [Burst], kept: [(time: Double, name: String)], settle: Double = 0.7,
                         maxLines: Int = 1000) -> String {
        let dir = frameDir.hasSuffix("/") ? String(frameDir.dropLast()) : frameDir
        let fps = duration > 0 ? Double(decodedFrames) / duration : 0
        var lines = [
            "# Motion timeline for \(videoPath)",
            "# \(String(format: "%.1f", duration))s, \(decodedFrames) frames decoded (\(String(format: "%.1f", fps)) fps average; screen recordings skip frames while the screen is still)",
        ]
        if bursts.isEmpty {
            lines.append("# No changes detected.")
        } else {
            lines.append("# Each line is a stretch where the screen changed (scene score > \(sceneThreshold); changes < \(burstGap)s apart merged;")
            lines.append("# the end includes the motion's low-level tail, so it's when the screen stopped moving).")
            lines.append("# To find something: Read the overview frames first (each shows the screen once its stretch settled),")
            lines.append("# then extract the stretch you care about frame by frame (command at the end).")
            lines.append("# start-end  length  peak  overview frame")
            for b in bursts.prefix(maxLines) {
                let frame = kept.first { $0.time >= b.start - 1e-6 && $0.time <= b.end + settle + 1e-6 }?.name ?? "-"
                let span = String(format: "%.2f-%.2fs", b.start, b.end)
                lines.append("  " + span.padding(toLength: max(13, span.count), withPad: " ", startingAt: 0)
                             + String(format: " %5.2fs  %.2f  ", b.end - b.start, b.peak) + frame)
            }
            if bursts.count > maxLines { lines.append("# … \(bursts.count - maxLines) more") }
        }
        let example = bursts.max { $0.end - $0.start < $1.end - $1.start }
        let from = max(0, (example?.start ?? 0) - 0.2), to = min(duration, (example?.end ?? 0.3) + settle)
        lines.append("# These are overview frames only; the original video is untouched. To see a stretch frame by frame")
        lines.append("# (every real frame, named by its time in ms: t010667ms.jpg = 10.667s), set -ss/-to to that stretch")
        lines.append("# (from 0.2s before its start to 0.7s after its end). Example, for the longest stretch:")
        lines.append("#   " + denseCommand(videoPath: videoPath, frameDir: dir, from: from, to: to))
        lines.append("# Screen recordings run at up to 60 fps during animations, so a 1s stretch can be 60 frames: Read selectively.")
        return lines.joined(separator: "\n") + "\n"
    }

    /// `t012.4s.jpg`
    static func frameName(time: Double) -> String {
        String(format: "t%05.1fs.jpg", max(0, time))
    }

    /// Indices (into the decoded frame sequence, ascending) of the frames to keep.
    /// - Frames with score > threshold are change candidates; candidates less than `burstGap`
    ///   apart form one burst (an animation, fade or scroll).
    /// - Each burst is represented by its **settled** frame: the last frame up to `settle` seconds
    ///   after the burst's last candidate, but before the next burst starts. (Slow fades keep
    ///   changing below the threshold after the last candidate.)
    /// - The first and last frames are always kept.
    /// - Fallback minimum rate: between two kept frames more than `fallbackGap` = max(5 s,
    ///   duration / 20) apart, a frame is added near every `fallbackGap` step, taken only from
    ///   quiet frames (not a candidate, not inside a burst). A variable-frame-rate recording has
    ///   no frames in a static stretch, so nothing is added there (the screen didn't change).
    /// - Over `maxFrames`: raise the threshold ×1.25 per step; past 1 (no scene candidates
    ///   left), subsample evenly keeping the first and last.
    static func selectFrames(_ frames: [Frame], duration: Double, threshold: Double = 0.08,
                             burstGap: Double = 0.5, settle: Double = 0.7, maxFrames: Int = 40) -> [Int] {
        guard !frames.isEmpty else { return [] }
        let frames = frames.sorted { $0.time < $1.time }
        let fallbackGap = max(5, duration / 20)
        var thr = threshold
        var picked = pick(frames, threshold: thr, burstGap: burstGap, settle: settle, fallbackGap: fallbackGap)
        while picked.count > maxFrames && thr <= 1 {
            thr *= 1.25
            picked = pick(frames, threshold: thr, burstGap: burstGap, settle: settle, fallbackGap: fallbackGap)
        }
        guard picked.count > maxFrames else { return picked }
        let m = max(maxFrames, 2), n = picked.count
        var even: [Int] = []          // `picked` is in time order, and so is this
        for k in 0..<m {
            let i = picked[Int((Double(k) * Double(n - 1) / Double(m - 1)).rounded())]
            if even.last != i { even.append(i) }
        }
        return even
    }

    typealias Burst = (start: Double, end: Double, peak: Double)

    /// Scores above this (but at or below the threshold) are low-level motion: the tail of a fade
    /// or scroll that changes too little per frame to be a candidate.
    static let motionFloor = 0.002
    /// Low-level motion extends a burst only while its frames are closer together than this.
    static let motionGap = 0.25

    /// Stretches where the screen changed: change candidates (score > threshold, first frame
    /// excluded) less than `burstGap` apart, merged, then extended through adjacent low-level
    /// motion (score > motionFloor, frames < motionGap apart), so a slow fade's tail counts
    /// toward its burst. `frames` sorted by time.
    static func bursts(_ frames: [Frame], threshold: Double = 0.08, burstGap: Double = 0.5) -> [Burst] {
        var bursts: [Burst] = []
        for f in frames.dropFirst() {
            if f.score > threshold {
                if let last = bursts.last, f.time - last.end < burstGap - 1e-6 {
                    bursts[bursts.count - 1].end = f.time
                    bursts[bursts.count - 1].peak = max(last.peak, f.score)
                } else {
                    bursts.append((f.time, f.time, f.score))
                }
            } else if f.score > motionFloor, let last = bursts.last, f.time - last.end < motionGap - 1e-6 {
                bursts[bursts.count - 1].end = f.time
            }
        }
        return bursts
    }

    /// `frames` sorted by time. Returns frame indices in time order.
    private static func pick(_ frames: [Frame], threshold: Double, burstGap: Double, settle: Double,
                             fallbackGap: Double) -> [Int] {
        let eps = 1e-6
        let bursts = bursts(frames, threshold: threshold, burstGap: burstGap)
        // Positions (into `frames`) of the kept frames, plus the busy span of each burst
        var kept: [Int] = [0]
        var busy: [ClosedRange<Double>] = []
        var cursor = 0
        for (b, burst) in bursts.enumerated() {
            let limit = b + 1 < bursts.count ? bursts[b + 1].start - eps : .infinity
            let target = min(burst.end + settle + eps, limit)
            while cursor < frames.count && frames[cursor].time < burst.end - eps { cursor += 1 }
            var rep = cursor                       // the burst's last candidate
            while rep + 1 < frames.count && frames[rep + 1].time <= target { rep += 1 }
            kept.append(rep)
            busy.append(burst.start...frames[rep].time)
        }
        kept.append(frames.count - 1)
        kept = Array(Set(kept)).sorted()

        // Fallback minimum rate in quiet stretches
        func isQuiet(_ i: Int) -> Bool {
            let f = frames[i]
            return f.score <= threshold && !busy.contains { $0.contains(f.time) }
        }
        /// Position of the quiet frame nearest `t`, strictly between positions `lo` and `hi`
        /// and within half a gap of `t`.
        func nearestQuiet(to t: Double, between lo: Int, _ hi: Int) -> Int? {
            var l = lo + 1, h = hi                 // first position with time >= t
            while l < h { let m = (l + h) / 2; if frames[m].time < t { l = m + 1 } else { h = m } }
            var best: Int?
            var left = l - 1, right = l
            while left > lo || right < hi {
                if right < hi, abs(frames[right].time - t) <= fallbackGap / 2 {
                    if isQuiet(right) { best = right; break }
                    right += 1
                } else { right = hi }
                if left > lo, abs(frames[left].time - t) <= fallbackGap / 2 {
                    if isQuiet(left) { best = left; break }
                    left -= 1
                } else { left = lo }
            }
            return best
        }
        var result: [Int] = []
        for (a, b) in zip(kept, kept.dropFirst()) {
            result.append(a)
            var t = frames[a].time + fallbackGap
            while frames[b].time - t > fallbackGap / 2 {
                if let q = nearestQuiet(to: t, between: result.last!, b),
                   frames[q].time - frames[result.last!].time > fallbackGap / 2 {
                    result.append(q)
                }
                t += fallbackGap
            }
        }
        result.append(kept.last!)
        return result.map { frames[$0].index }
    }

    /// Parses `metadata=print` output: "frame:N pts:… pts_time:T" then "lavfi.scene_score=S".
    static func parseAnalysis(_ output: String) -> [Frame] {
        var frames: [Frame] = []
        var current: (index: Int, time: Double)?
        for line in output.split(separator: "\n") {
            if line.hasPrefix("frame:") {
                var index: Int?, time: Double?
                for field in line.split(separator: " ", omittingEmptySubsequences: true) {
                    if field.hasPrefix("frame:") { index = Int(field.dropFirst(6)) }
                    else if field.hasPrefix("pts_time:") { time = Double(field.dropFirst(9)) }
                }
                if let index, let time { current = (index, time) } else { current = nil }
            } else if line.hasPrefix("lavfi.scene_score="), let c = current {
                frames.append((c.index, c.time, Double(line.dropFirst(18)) ?? 0))
                current = nil
            }
        }
        return frames
    }

    /// `scale` filter: long edge at most `edge`, aspect kept, even dimensions.
    static func scaleFilter(_ edge: Int) -> String {
        "scale='if(gt(iw,ih),min(\(edge),iw),-2)':'if(gt(iw,ih),-2,min(\(edge),ih))'"
    }

    // MARK: - Pipeline

    /// Runs the whole pipeline for `videoPath`, writing frames to `frameDir` and the poster to
    /// `posterPath`. Throws `Failure` if ffprobe/ffmpeg fail or no frames come out.
    static func process(videoPath: String, frameDir: String, posterPath: String) async throws -> Result {
        let started = Date()
        let fm = FileManager.default
        try fm.createDirectory(atPath: frameDir, withIntermediateDirectories: true)
        // ffmpeg's image writer doesn't create directories; the timeline's dense command targets this
        try fm.createDirectory(atPath: "\(frameDir)/dense", withIntermediateDirectories: true)

        // 1. Probe
        let probe = await run(ffprobe, ["-v", "error", "-show_entries", "format=duration:stream=codec_type",
                                        "-of", "json", videoPath], capture: .stdout)
        guard probe.status == 0,
              let json = try? JSONSerialization.jsonObject(with: Data(probe.output.utf8)) as? [String: Any] else {
            throw Failure(description: "ffprobe failed: \(probe.output.prefix(300))")
        }
        let streams = json["streams"] as? [[String: Any]] ?? []
        let hasVideo = streams.contains { $0["codec_type"] as? String == "video" }
        let hasAudio = streams.contains { $0["codec_type"] as? String == "audio" }
        guard hasVideo else { throw Failure(description: "no video stream") }
        var duration = Double((json["format"] as? [String: Any])?["duration"] as? String ?? "") ?? 0
        // Screen recordings hold their last frame until recording stops; ffprobe's duration ends at
        // the last frame, AVFoundation's (what Photos and the app show) includes the hold.
        if let av = try? await AVURLAsset(url: URL(fileURLWithPath: videoPath)).load(.duration).seconds,
           av.isFinite, av > duration {
            duration = av
        }

        // 2. Analysis pass
        let analysis = await run(ffmpeg, ["-v", "error", "-nostats", "-i", videoPath, "-an",
                                           "-vf", "\(scaleFilter(analysisEdge)),select='gte(scene,0)',metadata=print:file=-",
                                           "-f", "null", "-"], capture: .stdout)
        let frames = parseAnalysis(analysis.output)
        guard analysis.status == 0, !frames.isEmpty else {
            throw Failure(description: "analysis failed (status \(analysis.status), \(frames.count) frames)")
        }
        let t0 = frames.map(\.time).min() ?? 0
        if duration <= 0 { duration = (frames.map(\.time).max() ?? 0) - t0 }

        // 3. Select
        let chosen = selectFrames(frames, duration: duration, threshold: sceneThreshold,
                                  burstGap: burstGap, settle: settle, maxFrames: maxFrames)
        let timeByIndex = Dictionary(frames.map { ($0.index, $0.time) }, uniquingKeysWith: { a, _ in a })

        // 4. Extract (select by decoded-frame index; output order = index order)
        let expr = chosen.map { "eq(n,\($0))" }.joined(separator: "+")
        let tmpPattern = "\(frameDir)/.raw-%04d.jpg"
        let extract = await run(ffmpeg, ["-v", "error", "-nostats", "-i", videoPath, "-an",
                                          "-vf", "select='\(expr)',\(scaleFilter(frameEdge))",
                                          "-fps_mode", "vfr", "-q:v", "3", tmpPattern], capture: .stderr)
        guard extract.status == 0 else { throw Failure(description: "extraction failed: \(extract.output.prefix(300))") }

        var framePaths: [String] = []
        var keptFrames: [(time: Double, name: String)] = []
        for (k, index) in chosen.enumerated() {
            let raw = "\(frameDir)/.raw-\(String(format: "%04d", k + 1)).jpg"
            guard fm.fileExists(atPath: raw) else { continue }
            let base = frameName(time: (timeByIndex[index] ?? 0) - t0)
            var dest = "\(frameDir)/\(base)"
            var n = 2
            while fm.fileExists(atPath: dest) {
                dest = "\(frameDir)/\((base as NSString).deletingPathExtension)-\(n).jpg"
                n += 1
            }
            try fm.moveItem(atPath: raw, toPath: dest)
            framePaths.append(dest)
            keptFrames.append(((timeByIndex[index] ?? 0) - t0, (dest as NSString).lastPathComponent))
        }
        // Leftovers (shouldn't happen) don't belong in Claude's directory listing
        for leftover in (try? fm.contentsOfDirectory(atPath: frameDir)) ?? [] where leftover.hasPrefix(".raw-") {
            try? fm.removeItem(atPath: "\(frameDir)/\(leftover)")
        }
        guard let first = framePaths.first else { throw Failure(description: "no frames extracted") }

        // Motion timeline: every burst at the base threshold (times relative to the first frame)
        let sorted = frames.sorted { $0.time < $1.time }
        let allBursts = bursts(sorted, threshold: sceneThreshold, burstGap: burstGap)
            .map { (start: $0.start - t0, end: $0.end - t0, peak: $0.peak) }
        let timelineText = timeline(videoPath: videoPath, frameDir: frameDir, duration: duration,
                                    decodedFrames: frames.count, bursts: allBursts, kept: keptFrames, settle: settle)
        try timelineText.write(toFile: "\(frameDir)/\(timelineName)", atomically: true, encoding: .utf8)

        // Poster from the first frame
        let poster = await run(ffmpeg, ["-v", "error", "-y", "-i", first, "-vf", scaleFilter(posterEdge),
                                         "-q:v", "4", posterPath], capture: .stderr)
        if poster.status != 0 { try? fm.copyItem(atPath: first, toPath: posterPath) }

        // Audio, unless silent
        var audioPath: String?
        if hasAudio {
            let wav = "\(frameDir)/audio.wav"
            let audio = await run(ffmpeg, ["-v", "error", "-y", "-i", videoPath, "-vn", "-ac", "1", "-ar", "16000",
                                            "-c:a", "pcm_s16le", wav], capture: .stderr)
            if audio.status == 0, fm.fileExists(atPath: wav) {
                let vol = await run(ffmpeg, ["-nostats", "-i", wav, "-af", "volumedetect", "-f", "null", "-"], capture: .stderr)
                if let maxVol = parseMaxVolume(vol.output), maxVol <= silentMaxVolumeDB {
                    try? fm.removeItem(atPath: wav)
                } else {
                    audioPath = wav
                }
            }
        }

        let result = Result(videoPath: videoPath, duration: duration, frameDir: frameDir,
                            framePaths: framePaths, posterPath: posterPath, audioPath: audioPath,
                            changeCount: allBursts.count)
        print("[videos] \((videoPath as NSString).lastPathComponent): \(String(format: "%.1f", duration))s, "
              + "\(frames.count) decoded, \(framePaths.count) frames, \(allBursts.count) changes, audio=\(audioPath != nil ? "yes" : (hasAudio ? "silent" : "none")), "
              + "\(String(format: "%.1f", Date().timeIntervalSince(started)))s")
        return result
    }

    /// "max_volume: -91.0 dB" from volumedetect's log.
    static func parseMaxVolume(_ log: String) -> Double? {
        guard let range = log.range(of: #"max_volume: (-?[0-9.]+|-inf) dB"#, options: .regularExpression) else { return nil }
        let value = log[range].dropFirst("max_volume: ".count).dropLast(" dB".count)
        return value == "-inf" ? -.infinity : Double(value)
    }

    // MARK: - Processes

    enum Capture { case stdout, stderr }

    /// Runs a tool off the cooperative pool. Only the captured stream is piped (read fully before
    /// waiting); the other goes to /dev/null, so neither can fill up and deadlock.
    static func run(_ path: String, _ args: [String], capture: Capture) async -> (status: Int32, output: String) {
        await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                let process = Process()
                process.executableURL = URL(fileURLWithPath: path)
                process.arguments = args
                process.standardInput = FileHandle.nullDevice
                let pipe = Pipe()
                switch capture {
                case .stdout: process.standardOutput = pipe; process.standardError = FileHandle.nullDevice
                case .stderr: process.standardError = pipe; process.standardOutput = FileHandle.nullDevice
                }
                do { try process.run() } catch {
                    continuation.resume(returning: (-1, "\(error)"))
                    return
                }
                let timer = DispatchWorkItem { if process.isRunning { process.terminate() } }
                DispatchQueue.global().asyncAfter(deadline: .now() + processTimeout, execute: timer)
                let data = pipe.fileHandleForReading.readDataToEndOfFile()
                process.waitUntilExit()
                timer.cancel()
                continuation.resume(returning: (process.terminationStatus, String(decoding: data, as: UTF8.self)))
            }
        }
    }
}

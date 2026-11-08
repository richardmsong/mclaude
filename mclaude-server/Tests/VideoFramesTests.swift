import XCTest
@testable import mclaude_server

final class VideoFramesTests: XCTestCase {
    /// `count` frames at `fps`, all static (score 0) except the given index → score map.
    private func frames(count: Int, fps: Double = 10, changes: [Int: Double] = [:]) -> [VideoFrames.Frame] {
        (0..<count).map { (index: $0, time: Double($0) / fps, score: changes[$0] ?? 0) }
    }

    func testStaticShortVideoKeepsFirstAndLast() {
        let f = frames(count: 30)  // 3 s
        XCTAssertEqual(VideoFrames.selectFrames(f, duration: 3), [0, 29])
    }

    func testBurstIsRepresentedByItsSettledFrame() {
        // A scroll animation 1.0–1.4 s (frames 10…14), settled 0.7 s later (frame 21), and an
        // isolated change at 4.0 s (frame 40) settled at 4.7 s (frame 47)
        let f = frames(count: 60, changes: [10: 0.3, 11: 0.2, 12: 0.2, 13: 0.15, 14: 0.1, 40: 0.5])
        XCTAssertEqual(VideoFrames.selectFrames(f, duration: 6), [0, 21, 47, 59])
    }

    func testSettledFrameStopsBeforeNextBurst() {
        // Changes at 1.0 s and 1.5 s (0.5 s apart = separate bursts): the first is represented by
        // the last frame before the second starts
        let f = frames(count: 30, changes: [10: 0.3, 15: 0.3])
        XCTAssertEqual(VideoFrames.selectFrames(f, duration: 3), [0, 14, 22, 29])
    }

    func testVariableFrameRateBurstKeepsLastFrameWhenNothingFollows() {
        // Screen recordings only write frames when the screen changes: no frames after 2.1 s until 9 s
        let f: [VideoFrames.Frame] = [(0, 0, 0), (1, 2.0, 0.4), (2, 2.05, 0.02), (3, 2.1, 0.01), (4, 9.0, 0.3), (5, 9.5, 0)]
        XCTAssertEqual(VideoFrames.selectFrames(f, duration: 9.5), [0, 3, 5])
    }

    func testBelowThresholdIgnored() {
        let f = frames(count: 30, changes: [10: 0.08, 20: 0.05])
        XCTAssertEqual(VideoFrames.selectFrames(f, duration: 3), [0, 29])
    }

    func testFallbackGapFillsLongStaticStretch() {
        // 60 s static at 1 fps: gap = max(5, 60/20) = 5 → a frame every 5 s
        let f = frames(count: 61, fps: 1)
        let picked = VideoFrames.selectFrames(f, duration: 60)
        XCTAssertEqual(picked, [0, 5, 10, 15, 20, 25, 30, 35, 40, 45, 50, 55, 60])
        // Long video: gap = duration / 20
        let long = frames(count: 601, fps: 1)
        let gaps = zip(VideoFrames.selectFrames(long, duration: 600).dropFirst(), VideoFrames.selectFrames(long, duration: 600))
            .map { $0 - $1 }
        XCTAssertTrue(gaps.allSatisfy { $0 <= 30 })
    }

    func testFallbackSkipsFramesInsideATransition() {
        // 10 fps, 20 s static except a long scroll 9.0–11.0 s (a candidate every 0.2 s). The gap
        // fill aims for ~10 s, which is inside the scroll, so it must take a quiet frame instead.
        var changes: [Int: Double] = [:]
        for i in stride(from: 90, through: 110, by: 2) { changes[i] = 0.2 }
        let f = frames(count: 201, fps: 10, changes: changes)
        let picked = VideoFrames.selectFrames(f, duration: 20)
        XCTAssertTrue(picked.contains(117))                         // scroll settled at 11.7 s
        XCTAssertFalse(picked.contains { (90...117).contains($0) && $0 != 117 })
        XCTAssertTrue(picked.contains(50))                          // quiet stretch 0–9 s: a frame at 5 s
        XCTAssertEqual(picked, picked.sorted())
    }

    func testCapRaisesThresholdKeepingStrongestChanges() {
        // 100 isolated changes 1 s apart over 100 s; 20 strong ones, 80 weak
        var changes: [Int: Double] = [:]
        for k in 1...100 { changes[k * 10] = k % 5 == 0 ? 0.6 : 0.1 }
        let f = frames(count: 1010, changes: changes)
        let picked = VideoFrames.selectFrames(f, duration: 101)
        XCTAssertLessThanOrEqual(picked.count, 40)
        XCTAssertEqual(picked.first, 0)
        XCTAssertEqual(picked.last, 1009)
        // Each strong change is represented by its settled frame, 0.7 s later
        for k in stride(from: 5, through: 100, by: 5) { XCTAssertTrue(picked.contains(min(k * 10 + 7, 1009)), "strong change \(k)") }
        XCTAssertEqual(picked, picked.sorted())
    }

    func testSubsampleEvenlyWhenThresholdCannotFit() {
        // Every second is a very strong change (score 1.0 survives no threshold > 1, but gap fill
        // with a small maxFrames forces even subsampling)
        var changes: [Int: Double] = [:]
        for k in 1...50 { changes[k * 10] = 1.0 }
        let f = frames(count: 510, changes: changes)
        let picked = VideoFrames.selectFrames(f, duration: 51, maxFrames: 5)
        XCTAssertEqual(picked.count, 5)
        XCTAssertEqual(picked.first, 0)
        XCTAssertEqual(picked.last, 509)
        XCTAssertEqual(picked, picked.sorted())
    }

    func testEmpty() {
        XCTAssertEqual(VideoFrames.selectFrames([], duration: 0), [])
    }

    func testParseAnalysis() {
        let out = """
        frame:0    pts:0       pts_time:0
        lavfi.scene_score=0.000000
        frame:1    pts:1024    pts_time:0.1
        lavfi.scene_score=0.051186
        frame:2    pts:2048    pts_time:0.2
        lavfi.scene_score=0.302468
        """
        let f = VideoFrames.parseAnalysis(out)
        XCTAssertEqual(f.map(\.index), [0, 1, 2])
        XCTAssertEqual(f.map(\.time), [0, 0.1, 0.2])
        XCTAssertEqual(f[2].score, 0.302468, accuracy: 1e-9)
    }

    func testNamesAndAnnotation() {
        XCTAssertEqual(VideoFrames.frameName(time: 12.44), "t012.4s.jpg")
        XCTAssertEqual(VideoFrames.frameName(time: 0), "t000.0s.jpg")
        XCTAssertEqual(VideoFrames.frameName(time: 1234.5), "t1234.5s.jpg")
        let note = VideoFrames.annotation(duration: 12.36, frameCount: 14, frameDir: "/tmp/mclaude-screenshots/video-1",
                                          audioPath: "/tmp/mclaude-screenshots/video-1/audio.wav")
        XCTAssertEqual(note, "[video: 12.4s, 14 overview frames in /tmp/mclaude-screenshots/video-1/ named by timestamp, "
                       + "motion timeline /tmp/mclaude-screenshots/video-1/changes.txt, audio: /tmp/mclaude-screenshots/video-1/audio.wav; "
                       + "the original is untouched, extract any stretch at full frame rate with ffmpeg (command in changes.txt)]")
        XCTAssertFalse(note.dropLast().contains("]"))   // the app/titler match up to the first "]"
        XCTAssertEqual(VideoFrames.annotation(duration: 3, frameCount: 1, frameDir: "/d/", audioPath: nil),
                       "[video: 3.0s, 1 overview frame in /d/ named by timestamp, motion timeline /d/changes.txt; "
                       + "the original is untouched, extract any stretch at full frame rate with ffmpeg (command in changes.txt)]")
    }

    func testBurstsMergeAndTrackPeak() {
        let f = frames(count: 40, changes: [0: 0.9, 10: 0.2, 11: 0.4, 13: 0.1, 30: 0.09])
        let b = VideoFrames.bursts(f)
        XCTAssertEqual(b.count, 2)                      // frame 0 is never a change
        XCTAssertEqual(b[0].start, 1.0, accuracy: 1e-9)
        XCTAssertEqual(b[0].end, 1.3, accuracy: 1e-9)
        XCTAssertEqual(b[0].peak, 0.4, accuracy: 1e-9)
        XCTAssertEqual(b[1].start, 3.0, accuracy: 1e-9)
    }

    func testBurstExtendsThroughLowLevelMotion() {
        // A fade: one candidate at 1.0 s, then sub-threshold motion every 0.1 s until 1.6 s, then still
        let f = frames(count: 40, changes: [10: 0.2, 11: 0.03, 12: 0.02, 13: 0.01, 14: 0.01, 15: 0.005, 16: 0.003, 17: 0.001])
        let b = VideoFrames.bursts(f)
        XCTAssertEqual(b.count, 1)
        XCTAssertEqual(b[0].end, 1.6, accuracy: 1e-9)
        // Low-level motion alone (no candidate) is not a burst, and doesn't bridge a gap > motionGap
        XCTAssertTrue(VideoFrames.bursts(frames(count: 40, changes: [5: 0.05, 6: 0.05])).isEmpty)
        let gapped = VideoFrames.bursts(frames(count: 40, fps: 2, changes: [2: 0.2, 3: 0.05]))  // 0.5 s apart
        XCTAssertEqual(gapped[0].end, 1.0, accuracy: 1e-9)
    }

    func testTimelineListsEveryBurstWithItsOverviewFrame() {
        let text = VideoFrames.timeline(videoPath: "/v/video-1.mov", frameDir: "/v/video-1/", duration: 36.2, decodedFrames: 912,
                                        bursts: [(10.62, 11.38, 0.31), (27.9, 28.02, 0.09)],
                                        kept: [(0, "t000.0s.jpg"), (11.5, "t011.5s.jpg"), (36.2, "t036.2s.jpg")])
        let lines = text.split(separator: "\n").map(String.init)
        XCTAssertEqual(lines[0], "# Motion timeline for /v/video-1.mov")
        XCTAssertTrue(lines[1].contains("912 frames decoded (25.2 fps average"))
        XCTAssertEqual(lines[7], "  10.62-11.38s   0.76s  0.31  t011.5s.jpg")
        XCTAssertEqual(lines[8], "  27.90-28.02s   0.12s  0.09  -")
        XCTAssertTrue(text.contains("mkdir -p /v/video-1/dense && ffmpeg -v error -ss 10.4 -to 12.1 -copyts -i /v/video-1.mov "
                                    + "-fps_mode passthrough -frame_pts 1 -enc_time_base 1:1000"))
        XCTAssertTrue(text.contains("/v/video-1/dense/t%06dms.jpg"))
        let empty = VideoFrames.timeline(videoPath: "/v/a.mov", frameDir: "/v/a", duration: 3, decodedFrames: 90, bursts: [], kept: [])
        XCTAssertTrue(empty.contains("# No changes detected."))
    }

    func testParseMaxVolume() {
        XCTAssertEqual(VideoFrames.parseMaxVolume("[Parsed_volumedetect_0 @ 0x1] max_volume: -91.0 dB"), -91.0)
        XCTAssertEqual(VideoFrames.parseMaxVolume("max_volume: -3.2 dB"), -3.2)
        XCTAssertNil(VideoFrames.parseMaxVolume("nothing"))
    }

    func testByteRange() {
        XCTAssertEqual(parseByteRange("bytes=0-1", size: 100), 0...1)
        XCTAssertEqual(parseByteRange("bytes=10-", size: 100), 10...99)
        XCTAssertEqual(parseByteRange("bytes=-10", size: 100), 90...99)
        XCTAssertEqual(parseByteRange("bytes=-500", size: 100), 0...99)
        XCTAssertEqual(parseByteRange("bytes=50-500", size: 100), 50...99)
        XCTAssertNil(parseByteRange("bytes=100-", size: 100))
        XCTAssertNil(parseByteRange("bytes=5-1", size: 100))
        XCTAssertNil(parseByteRange("bytes=0-1,5-6", size: 100))
        XCTAssertNil(parseByteRange("items=0-1", size: 100))
    }

    func testTitlerIgnoresVideoNote() {
        let msg = "/tmp/mclaude-screenshots/video-1730000000000000.mov [video: 12.4s, 14 frames in /tmp/mclaude-screenshots/video-1730000000000000/ named by timestamp, audio: /tmp/mclaude-screenshots/video-1730000000000000/audio.wav] why does this flicker"
        XCTAssertEqual(SessionTitler.typedText(of: msg), "why does this flicker")
        let only = "/tmp/mclaude-screenshots/video-1.mp4 [video: 3.0s, 2 frames in /tmp/mclaude-screenshots/video-1/ named by timestamp]"
        XCTAssertEqual(SessionTitler.typedText(of: only), "")
        XCTAssertTrue(SessionTitler.shouldInferTitle(fromFirstMessage: only))
    }
}

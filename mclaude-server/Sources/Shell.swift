import Foundation

// Helper to run a process and capture output
enum Shell {
    static func syncExec(_ path: String, arguments: [String]) -> (Bool, String) {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: path)
        process.arguments = arguments
        let pipe = Pipe()
        let errPipe = Pipe()
        process.standardOutput = pipe
        process.standardError = errPipe

        do {
            try process.run()
        } catch {
            return (false, "")
        }

        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        let errData = errPipe.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()

        let output = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .newlines) ?? ""
        let stderr = String(data: errData, encoding: .utf8)?.trimmingCharacters(in: .newlines) ?? ""
        if !stderr.isEmpty {
            print("[shell] stderr: \(stderr)")
        }
        return (process.terminationStatus == 0, output)
    }

    static func exec(_ path: String, arguments: [String]) async -> String {
        await withCheckedContinuation { continuation in
            DispatchQueue.global().async {
                let (_, output) = syncExec(path, arguments: arguments)
                continuation.resume(returning: output)
            }
        }
    }
}

/// Where Claude Code keeps a session's transcript. Claude resolves symlinks in the cwd and
/// replaces every non-alphanumeric character with "-" (so /tmp/a.b -> -private-tmp-a-b).
/// Older code only replaced "/", so that spelling is tried as a fallback.
enum ClaudePaths {
    static let projectsDir = "\(NSHomeDirectory())/.claude/projects"

    static func encode(_ path: String) -> String {
        String(path.unicodeScalars.map { CharacterSet.alphanumerics.contains($0) && $0.isASCII ? Character($0) : "-" })
    }

    static func transcript(projectsDir: String = projectsDir, cwd: String, sessionId: String) -> String {
        // realpath(3), not NSString.resolvingSymlinksInPath: the latter strips "/private" back off
        let real = realpath(cwd, nil).map { p in defer { free(p) }; return String(cString: p) } ?? cwd
        let candidates = [encode(real), encode(cwd), cwd.replacingOccurrences(of: "/", with: "-")]
            .map { "\(projectsDir)/\($0)/\(sessionId).jsonl" }
        return candidates.first { FileManager.default.fileExists(atPath: $0) } ?? candidates[0]
    }
}

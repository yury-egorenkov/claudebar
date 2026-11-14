// ClaudeBar: Claude Code plan usage limits in the macOS menu bar.
//
// This file only renders. The numbers come from the Go helper next to this binary
// (`Contents/MacOS/claudebar-cli json`): the usage API (token sent to api.anthropic.com
// only) and the Claude Code status line.

import AppKit
import ServiceManagement
import SwiftUI

// MARK: - Data from `claudebar-cli json`

// By how early the limit runs out at the current pace (the Go helper decides):
// ok lasts until the reset; soon up to a day early (yellow); warn more than a day (orange);
// bad more than 2 days or under 10% left (red). For the 5-hour window, in proportion.
enum Level: String, Decodable {
    case unknown, ok, soon, warn, bad

    // On pace the app stays quiet: system accent in the panel, monochrome in the menu bar.
    var color: Color { self == .ok ? .accentColor : Color(nsColor: nsColor) }

    var nsColor: NSColor {
        switch self {
        case .ok: return .controlAccentColor
        case .soon: return .systemYellow
        case .warn: return .systemOrange
        case .bad: return .systemRed
        case .unknown: return .systemGray
        }
    }

    var alarming: Bool { self == .soon || self == .warn || self == .bad }

    var label: String {
        switch self {
        case .soon: return "Tight"
        case .warn: return "Ahead of pace"
        case .bad: return "Slow down"
        default: return ""
        }
    }
}

struct LimitView: Decodable {
    var has = false
    var left: Double = 0
    var evenLeft: Double = 0
    var resetsAt: Int64 = 0
    var runOutAt: Int64 = 0
    var perDay: Double = 0
    var level: Level = .unknown
}

struct ModelView: Decodable, Identifiable {
    var name = ""
    var left: Double = 0
    var resetsAt: Int64 = 0
    var id: String { name }
}

// One day of the weekly window (24 h from the reset time); state: past, today, future.
struct DayView: Decodable, Identifiable {
    var start: Int64 = 0
    var state = "future"
    var spent: Double = 0
    var known = false
    var id: Int64 { start }
}

// Even spend: the same share of the week every day.
let dayBudget = 100.0 / 7

struct Snapshot: Decodable {
    var connected = true
    var updatedAt: Int64 = 0
    var level: Level = .unknown
    var badge = "–"
    var week = LimitView()
    var fiveHour = LimitView()
    var models: [ModelView] = []
    var days: [DayView] = []
    var error = ""
}

// MARK: - Model

final class Model: ObservableObject {
    @Published var snapshot = Snapshot()
    @Published var error: String?

    private var timer: Timer?
    private let helper = Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/claudebar-cli")

    init() {
        refresh()
        let timer = Timer(timeInterval: 20, repeats: true) { [weak self] _ in self?.refresh() }
        RunLoop.main.add(timer, forMode: .common)
        self.timer = timer
    }

    func refresh() {
        do {
            let data = try runHelper(["json"])
            let decoder = JSONDecoder()
            decoder.keyDecodingStrategy = .convertFromSnakeCase
            snapshot = try decoder.decode(Snapshot.self, from: data)
            error = nil
        } catch {
            self.error = "\(error)"
        }
    }

    func connect() {
        do {
            _ = try runHelper(["connect"])
            refresh()
        } catch {
            self.error = "\(error)"
        }
    }

    var startAtLogin: Bool {
        get { SMAppService.mainApp.status == .enabled }
        set {
            do {
                if newValue {
                    try SMAppService.mainApp.register()
                } else {
                    try SMAppService.mainApp.unregister()
                }
            } catch {
                self.error = "Start at Login: \(error.localizedDescription)"
            }
            objectWillChange.send()
        }
    }

    // The helper finishes in milliseconds, so it runs on the main thread,
    // with a 5 s limit so a stuck helper can never freeze the menu bar.
    private func runHelper(_ args: [String]) throws -> Data {
        // The helper must be a separate file, never this app (APFS ignores case:
        // "claudebar" would be "ClaudeBar").
        if let app = Bundle.main.executableURL, sameFile(app, helper) {
            throw HelperError(message: "helper \(helper.path) is the app itself; rebuild with make install")
        }
        let proc = Process()
        proc.executableURL = helper
        proc.arguments = args
        let out = Pipe()
        let err = Pipe()
        proc.standardOutput = out
        proc.standardError = err
        let done = DispatchSemaphore(value: 0)
        proc.terminationHandler = { _ in done.signal() }
        try proc.run()
        if done.wait(timeout: .now() + 5) == .timedOut {
            proc.terminate()
            throw HelperError(message: "claudebar-cli \(args.joined(separator: " ")) did not finish in 5 s")
        }
        let data = out.fileHandleForReading.readDataToEndOfFile()
        if proc.terminationStatus != 0 {
            let msg = String(data: err.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
            throw HelperError(message: msg.trimmingCharacters(in: .whitespacesAndNewlines))
        }
        return data
    }
}

func sameFile(_ one: URL, _ two: URL) -> Bool {
    let ids = [one, two].map { try? $0.resourceValues(forKeys: [.fileResourceIdentifierKey]).fileResourceIdentifier }
    guard let first = ids[0], let second = ids[1] else { return false }
    return first.isEqual(second)
}

struct HelperError: Error, CustomStringConvertible {
    var message: String
    var description: String { message.isEmpty ? "claudebar helper failed" : message }
}

// MARK: - App

@main
struct ClaudeBarApp: App {
    @StateObject private var model = Model()

    // Two guards against self-spawning (once, a helper named "claudebar" overwrote
    // "ClaudeBar" on case-insensitive APFS; every copy started another):
    // 1. The app takes no arguments: started with any (e.g. "json"), it exits at once.
    // 2. One copy only: if ClaudeBar is already running, a new copy exits.
    init() {
        if CommandLine.arguments.count > 1 { exit(2) }
        let me = ProcessInfo.processInfo.processIdentifier
        let others = NSRunningApplication
            .runningApplications(withBundleIdentifier: Bundle.main.bundleIdentifier ?? "com.claudebar.app")
            .filter { $0.processIdentifier != me }
        if !others.isEmpty { exit(0) }
    }

    var body: some Scene {
        MenuBarExtra {
            Panel(model: model)
        } label: {
            Image(nsImage: badgeImage(text: model.snapshot.badge, level: model.snapshot.level))
        }
        .menuBarExtraStyle(.window)
    }
}

// MARK: - Menu bar badge

// A rounded rect with the weekly % left inside. On pace: outline, template image,
// so macOS tints it like the system icons. Running out early: filled yellow, orange or red.
func badgeImage(text: String, level: Level) -> NSImage {
    let font = NSFont.monospacedDigitSystemFont(ofSize: 11, weight: .semibold)
    let alarming = level.alarming
    let fill = level.nsColor
    // Dark text on yellow and orange, white on red; the template image is plain black.
    let textColor: NSColor = level == .bad ? .white : (alarming ? NSColor.black.withAlphaComponent(0.85) : .black)
    let attrs: [NSAttributedString.Key: Any] = [.font: font, .foregroundColor: textColor]
    let textSize = (text as NSString).size(withAttributes: attrs)
    let size = NSSize(width: max(ceil(textSize.width) + 10, 26), height: 16)

    let image = NSImage(size: size, flipped: false) { rect in
        if alarming {
            fill.setFill()
            NSBezierPath(roundedRect: rect, xRadius: 4, yRadius: 4).fill()
        } else {
            NSColor.black.setStroke()
            let path = NSBezierPath(roundedRect: rect.insetBy(dx: 0.75, dy: 0.75), xRadius: 3.5, yRadius: 3.5)
            path.lineWidth = 1.5
            path.stroke()
        }
        let origin = NSPoint(x: (rect.width - textSize.width) / 2, y: (rect.height - textSize.height) / 2)
        (text as NSString).draw(at: origin, withAttributes: attrs)
        return true
    }
    image.isTemplate = !alarming
    return image
}

// MARK: - Panel

struct Panel: View {
    @ObservedObject var model: Model

    var body: some View {
        let snap = model.snapshot

        VStack(alignment: .leading, spacing: 0) {
            Header(snap: snap)
                .padding(.horizontal, 14)

            Divider().padding(.vertical, 10)

            Group {
                if !snap.connected {
                    ConnectCard(model: model)
                } else if !snap.week.has {
                    EmptyCard()
                } else {
                    Limits(snap: snap)
                }

                ForEach([snap.error, model.error ?? ""].filter { !$0.isEmpty }, id: \.self) { error in
                    Text(error)
                        .font(.system(size: 11))
                        .foregroundStyle(.secondary)
                        .lineLimit(3)
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(.top, 10)
                }
            }
            .padding(.horizontal, 14)

            Divider().padding(.vertical, 8)

            Actions(model: model, updatedAt: snap.updatedAt)
                .padding(.horizontal, 6)
        }
        .padding(.vertical, 12)
        .frame(width: 320)
        .onAppear { model.refresh() }
    }
}

struct Header: View {
    var snap: Snapshot

    var body: some View {
        HStack(alignment: .center) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Claude usage")
                    .font(.system(size: 15, weight: .semibold))
                Text(snap.updatedAt > 0 ? "Updated \(ago(snap.updatedAt))" : "No data yet")
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
            }
            Spacer()
            if snap.level.alarming || (snap.week.has && snap.week.left <= 0) {
                StatusPill(level: snap.level, label: snap.week.has && snap.week.left <= 0 ? "Limit reached" : snap.level.label)
            }
        }
    }
}

struct StatusPill: View {
    var level: Level
    var label: String

    var body: some View {
        HStack(spacing: 6) {
            Circle().fill(level.color).frame(width: 7, height: 7)
            Text(label).font(.system(size: 12, weight: .medium))
        }
        .padding(.horizontal, 9)
        .padding(.vertical, 4)
        .background(Capsule().fill(level.color.opacity(0.15)))
    }
}

struct Limits: View {
    var snap: Snapshot

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("PLAN USAGE LIMITS")
                .font(.system(size: 11, weight: .semibold))
                .kerning(0.4)
                .foregroundStyle(.secondary)

            VStack(alignment: .leading, spacing: 8) {
                LimitRow(title: "Weekly · all models", left: snap.week.left, resetsAt: snap.week.resetsAt,
                         evenLeft: snap.week.evenLeft, color: snap.week.level.color)
                if snap.days.count == 7 {
                    DayStrip(days: snap.days)
                        .padding(.top, 2)
                }
                if let forecast {
                    Text(forecast)
                        .font(.system(size: 12, weight: .medium))
                        .foregroundStyle(snap.week.level.color)
                }
                Text(dayNote)
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
            }

            ForEach(snap.models) { model in
                LimitRow(title: "Weekly · \(model.name)", left: model.left, resetsAt: model.resetsAt, color: .accentColor)
            }

            if snap.fiveHour.has {
                LimitRow(title: "5-hour session", left: snap.fiveHour.left, resetsAt: snap.fiveHour.resetsAt,
                         color: snap.fiveHour.level.color)
            }
        }
    }

    private var forecast: String? {
        let week = snap.week
        if week.left <= 0 { return "Used up until the reset" }
        if week.runOutAt == 0 { return nil }
        return "Runs out \(when(week.runOutAt)) at this pace"
    }

    // "Today 3% of 14% · 15% a day for 4 days"
    private var dayNote: String {
        let week = snap.week
        let secs = max(0, TimeInterval(week.resetsAt) - Date().timeIntervalSince1970)
        let daysLeft = max(1, Int((secs / 86400).rounded(.up)))
        let budget = "\(percent(week.perDay)) a day for \(daysLeft) \(daysLeft == 1 ? "day" : "days")"
        guard let today = snap.days.first(where: { $0.state == "today" }), today.known else { return budget }
        return "Today \(percent(today.spent)) of \(percent(dayBudget)) · \(budget)"
    }
}

// Seven cells, one per day of the weekly window. Like the limit bars, a cell shows what
// is LEFT of the day's budget (100/7 % of the week), as a fill emptying from the right and
// as a number inside. Over budget: empty on orange, over twice the budget red. Future: 100%.
struct DayStrip: View {
    var days: [DayView]

    var body: some View {
        HStack(spacing: 4) {
            ForEach(days) { day in
                DayCell(day: day)
            }
        }
    }
}

struct DayCell: View {
    var day: DayView

    var body: some View {
        let today = day.state == "today"
        let spent = day.known ? day.spent / dayBudget : 0
        let left = day.known || day.state == "future" ? max(0, 1 - spent) : 0
        let track: Color = spent > 2 ? Color(nsColor: .systemRed).opacity(0.55)
            : spent > 1 ? Color(nsColor: .systemOrange).opacity(0.55)
            : Color.primary.opacity(0.08)
        // % of the day's budget left, inside the cell; "–" without data.
        let label = day.known || day.state == "future" ? percent(left * 100) : "–"

        VStack(spacing: 3) {
            Text(weekday(day.start))
                .font(.system(size: 10, weight: today ? .semibold : .regular))
                .foregroundStyle(today ? HierarchicalShapeStyle.primary : HierarchicalShapeStyle.secondary)
            GeometryReader { geo in
                let fill = geo.size.width * min(left, 1)
                let text = Text(label).font(.system(size: 10, weight: .semibold)).monospacedDigit()

                ZStack(alignment: .leading) {
                    RoundedRectangle(cornerRadius: 4)
                        .fill(track)
                    RoundedRectangle(cornerRadius: 4)
                        .fill(Color.accentColor)
                        .frame(width: fill)
                    // The number sits across the fill edge: white over the fill,
                    // regular text over the empty part.
                    text
                        .foregroundStyle(.primary)
                        .frame(width: geo.size.width)
                    text
                        .foregroundStyle(.white)
                        .frame(width: geo.size.width)
                        .mask(alignment: .leading) { Rectangle().frame(width: fill) }
                }
            }
            .frame(height: 16)
            .overlay(
                RoundedRectangle(cornerRadius: 4)
                    .stroke(Color.primary.opacity(today ? 0.45 : 0), lineWidth: 1)
            )
        }
        .frame(maxWidth: .infinity)
        .help(day.known ? "\(percent(day.spent)) of the week spent this day, budget \(percent(dayBudget))" : (day.state == "future" ? "Budget \(percent(dayBudget)) of the week" : "No data for this day"))
    }
}

// Title, "% left · Resets 6d", a thin bar; for the week also a tick at the even pace.
struct LimitRow: View {
    var title: String
    var left: Double
    var resetsAt: Int64
    var evenLeft: Double? = nil
    var color: Color

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            HStack(alignment: .firstTextBaseline) {
                Text(title).font(.system(size: 14))
                Spacer()
                Text("\(percent(left)) left · Resets \(short(resetsAt))")
                    .font(.system(size: 13))
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
            }
            UsageBar(left: left, evenLeft: evenLeft, color: color)
        }
        .help("Resets \(when(resetsAt))")
    }
}

// Track with the % left filled. The tick marks what would be left at an even pace:
// a fill shorter than the tick means spending ahead of pace.
struct UsageBar: View {
    var left: Double
    var evenLeft: Double?
    var color: Color

    var body: some View {
        GeometryReader { geo in
            let width = geo.size.width
            let fill = width * min(max(left, 0), 100) / 100

            // Every layer has an explicit height: the taller tick must not stretch the bar.
            ZStack(alignment: .leading) {
                Capsule()
                    .fill(Color.primary.opacity(0.1))
                    .frame(height: 6)
                Capsule()
                    .fill(color)
                    .frame(width: left > 0 ? max(fill, 6) : 0, height: 6)
                if let evenLeft {
                    let tick = width * min(max(evenLeft, 0), 100) / 100
                    Capsule()
                        .fill(Color.primary.opacity(0.55))
                        .frame(width: 2, height: 12)
                        .offset(x: min(max(tick - 1, 0), width - 2))
                }
            }
            .frame(width: width, height: 6)
            .animation(.easeOut(duration: 0.25), value: left)
        }
        .frame(height: 6)
    }
}

struct ConnectCard: View {
    @ObservedObject var model: Model

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Not connected")
                .font(.system(size: 15, weight: .semibold))
            Text("No Claude Code login found. Connecting adds the status line to ~/.claude/settings.json (with a backup).")
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Button("Connect to Claude Code") { model.connect() }
                .buttonStyle(.borderedProminent)
        }
    }
}

struct EmptyCard: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Waiting for data")
                .font(.system(size: 15, weight: .semibold))
            Text("Limits appear after the next Claude Code reply or usage check.")
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }
}

// Menu-style rows, like a native menu.
struct Actions: View {
    @ObservedObject var model: Model
    var updatedAt: Int64

    var body: some View {
        VStack(spacing: 0) {
            MenuRow(title: "Refresh") { model.refresh() }
            MenuRow(title: "Start at Login", checked: model.startAtLogin) { model.startAtLogin.toggle() }
            Divider().padding(.vertical, 6).padding(.horizontal, 8)
            MenuRow(title: "ClaudeBar by egorsky.com") {
                if let url = URL(string: "https://egorsky.com") { NSWorkspace.shared.open(url) }
            }
            MenuRow(title: "Quit ClaudeBar") { NSApp.terminate(nil) }
        }
    }
}

// Every row keeps the checkmark column, so titles line up like in a native menu.
struct MenuRow: View {
    var title: String
    var checked = false
    var action: () -> Void
    @State private var hover = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 6) {
                Image(systemName: "checkmark")
                    .font(.system(size: 11, weight: .semibold))
                    .opacity(checked ? 1 : 0)
                    .frame(width: 12)
                Text(title).font(.system(size: 14))
                Spacer()
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 5)
            .contentShape(Rectangle())
            .background(RoundedRectangle(cornerRadius: 6).fill(hover ? Color.primary.opacity(0.08) : .clear))
        }
        .buttonStyle(.plain)
        // No keyboard focus ring: the panel opens with the mouse, like a menu.
        .focusable(false)
        .onHover { hover = $0 }
    }
}

// MARK: - Formatting (English, 24-hour)

func percent(_ val: Double) -> String { "\(Int(val.rounded()))%" }

func when(_ epoch: Int64) -> String {
    let date = Date(timeIntervalSince1970: TimeInterval(epoch))
    let fmt = DateFormatter()
    fmt.locale = Locale(identifier: "en_GB")
    fmt.dateFormat = Calendar.current.isDateInToday(date) ? "HH:mm" : "EEE HH:mm"
    return fmt.string(from: date)
}

func weekday(_ epoch: Int64) -> String {
    let fmt = DateFormatter()
    fmt.locale = Locale(identifier: "en_GB")
    fmt.dateFormat = "EEE"
    return fmt.string(from: Date(timeIntervalSince1970: TimeInterval(epoch)))
}

// "6d", "5h", "40m", like the Claude app.
func short(_ epoch: Int64) -> String {
    let secs = max(0, Int(TimeInterval(epoch) - Date().timeIntervalSince1970))
    if secs >= 86400 { return "\(secs / 86400)d" }
    if secs >= 3600 { return "\(secs / 3600)h" }
    return "\(max(1, secs / 60))m"
}

func ago(_ epoch: Int64) -> String {
    let secs = max(0, Int(Date().timeIntervalSince1970 - TimeInterval(epoch)))
    if secs < 60 { return "just now" }
    if secs < 3600 { return "\(secs / 60) min ago" }
    if secs < 172800 { return "\(secs / 3600) h ago" }
    return "\(secs / 86400) d ago"
}

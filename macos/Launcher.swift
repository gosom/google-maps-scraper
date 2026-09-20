import Cocoa
import WebKit

final class AppDelegate: NSObject, NSApplicationDelegate, WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate {
    var window: NSWindow!
    var web: WKWebView!
    var server: Process?
    var attempts = 0
    let home = FileManager.default.homeDirectoryForCurrentUser
    var port: UInt16 = 8080
    var url: URL { URL(string: "http://127.0.0.1:\(port)")! }

    // Returns true if 127.0.0.1:port can be bound right now.
    static func isPortFree(_ port: UInt16) -> Bool {
        let fd = socket(AF_INET, SOCK_STREAM, 0)
        guard fd >= 0 else { return false }
        defer { close(fd) }
        var addr = sockaddr_in()
        addr.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        addr.sin_family = sa_family_t(AF_INET)
        addr.sin_port = port.bigEndian
        addr.sin_addr.s_addr = inet_addr("127.0.0.1")
        let result = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.bind(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) }
        }
        return result == 0
    }

    // Prefer 8080; fall back to the next free port so a stale server never blocks launch.
    static func pickPort() -> UInt16? {
        for candidate in UInt16(8080)...UInt16(8199) where isPortFree(candidate) { return candidate }
        return nil
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        // Single instance: if another copy is already running, bring it forward and exit.
        let myPID = ProcessInfo.processInfo.processIdentifier
        if let bundleID = Bundle.main.bundleIdentifier,
           let other = NSRunningApplication.runningApplications(withBundleIdentifier: bundleID).first(where: { $0.processIdentifier != myPID }) {
            other.activate()
            NSApp.terminate(nil)
            return
        }
        guard let freePort = AppDelegate.pickPort() else {
            let alert = NSAlert()
            alert.messageText = "NNH Maps Scraper"
            alert.informativeText = "No free local port between 8080 and 8199."
            alert.runModal()
            NSApp.terminate(nil)
            return
        }
        port = freePort
        let menu = NSMenu()
        let appItem = NSMenuItem()
        menu.addItem(appItem)
        let appMenu = NSMenu()
        appMenu.addItem(withTitle: "Quit NNH Maps Scraper", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        appItem.submenu = appMenu
        let editItem = NSMenuItem(title: "Edit", action: nil, keyEquivalent: "")
        let edit = NSMenu(title: "Edit")
        for (title, selector, key) in [("Cut", "cut:", "x"), ("Copy", "copy:", "c"), ("Paste", "paste:", "v"), ("Select All", "selectAll:", "a")] {
            edit.addItem(withTitle: title, action: Selector(selector), keyEquivalent: key)
        }
        editItem.submenu = edit
        menu.addItem(editItem)
        NSApp.mainMenu = menu
        let configuration = WKWebViewConfiguration()
        // Preserve table fragments when HTMX replaces tbody in WebKit.
        configuration.userContentController.addUserScript(WKUserScript(source: "if (window.htmx) htmx.config.useTemplateFragments = true;", injectionTime: .atDocumentEnd, forMainFrameOnly: true))
        // Deep Black theme from Contents/Resources/theme.css, injected before first paint.
        if let themeURL = Bundle.main.url(forResource: "theme", withExtension: "css"),
           let css = try? String(contentsOf: themeURL, encoding: .utf8),
           let cssJSON = try? JSONSerialization.data(withJSONObject: [css]),
           let cssArray = String(data: cssJSON, encoding: .utf8) {
            let js = "(function(){var s=document.createElement('style');s.id='deep-black-theme';s.textContent=\(cssArray)[0];document.documentElement.appendChild(s);})();"
            configuration.userContentController.addUserScript(WKUserScript(source: js, injectionTime: .atDocumentStart, forMainFrameOnly: true))
        }
        web = WKWebView(frame: .zero, configuration: configuration)
        web.underPageBackgroundColor = .black
        web.setValue(false, forKey: "drawsBackground")
        web.navigationDelegate = self
        web.uiDelegate = self
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1200, height: 800), styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
        window.title = "NNH Maps Scraper"
        window.appearance = NSAppearance(named: .darkAqua)
        window.backgroundColor = .black
        window.contentView = web
        window.center()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        web.loadHTMLString("<html><body style='font:24px system-ui;padding:50px;background:#000;color:#ededed'>Starting NNH Maps Scraper…</body></html>", baseURL: nil)
        let process = Process()
        process.executableURL = Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/google_maps_scraper")
        process.arguments = ["-web", "-addr", "127.0.0.1:\(port)", "-data-folder", home.appendingPathComponent("webdata").path]
        process.currentDirectoryURL = home
        let log = home.appendingPathComponent("google_maps_scraper.log")
        do {
            if !FileManager.default.fileExists(atPath: log.path) { FileManager.default.createFile(atPath: log.path, contents: nil) }
            let handle = try FileHandle(forWritingTo: log)
            try handle.seekToEnd()
            process.standardOutput = handle
            process.standardError = handle
            try process.run()
            server = process
            waitForServer()
        } catch { showError(error.localizedDescription) }
    }

    func waitForServer() {
        guard server?.isRunning == true else { showError("The server could not start. Another copy may be running. See ~/google_maps_scraper.log."); return }
        attempts += 1
        var request = URLRequest(url: url)
        request.timeoutInterval = 1
        URLSession.shared.dataTask(with: request) { _, response, _ in
            DispatchQueue.main.async {
                guard self.server?.isRunning == true else { self.showError("The server stopped. See ~/google_maps_scraper.log."); return }
                if (response as? HTTPURLResponse)?.statusCode == 200 { self.web.load(URLRequest(url: self.url)) }
                else if self.attempts < 120 { DispatchQueue.main.asyncAfter(deadline: .now() + 1) { self.waitForServer() } }
                else { self.showError("The server did not become ready. See ~/google_maps_scraper.log.") }
            }
        }.resume()
    }

    func showError(_ message: String) {
        let alert = NSAlert()
        alert.messageText = "NNH Maps Scraper"
        alert.informativeText = message
        alert.runModal()
        NSApp.terminate(nil)
    }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }
    func applicationWillTerminate(_ notification: Notification) { if server?.isRunning == true { server?.terminate() } }

    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction, decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        guard let destination = action.request.url else { decisionHandler(.cancel); return }
        if destination.scheme == "about" { decisionHandler(.allow); return }
        if destination.host == url.host && destination.port == url.port {
            if action.shouldPerformDownload { decisionHandler(.download) }
            else if action.targetFrame == nil { webView.load(action.request); decisionHandler(.cancel) }
            else { decisionHandler(.allow) }
        } else {
            if ["https", "http"].contains(destination.scheme ?? "") { NSWorkspace.shared.open(destination) }
            decisionHandler(.cancel)
        }
    }
    func webView(_ webView: WKWebView, decidePolicyFor response: WKNavigationResponse, decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void) {
        decisionHandler(response.canShowMIMEType ? .allow : .download)
    }
    func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) { download.delegate = self }
    func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) { download.delegate = self }
    func download(_ download: WKDownload, decideDestinationUsing response: URLResponse, suggestedFilename: String, completionHandler: @escaping (URL?) -> Void) {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = suggestedFilename
        panel.beginSheetModal(for: window) { result in completionHandler(result == .OK ? panel.url : nil) }
    }
    func download(_ download: WKDownload, didFailWithError error: Error, resumeData: Data?) {
        let alert = NSAlert()
        alert.messageText = "Download failed"
        alert.informativeText = error.localizedDescription
        alert.beginSheetModal(for: window)
    }
    func webView(_ webView: WKWebView, runJavaScriptConfirmPanelWithMessage message: String, initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping (Bool) -> Void) {
        let alert = NSAlert()
        alert.messageText = message
        alert.addButton(withTitle: "OK")
        alert.addButton(withTitle: "Cancel")
        alert.beginSheetModal(for: window) { completionHandler($0 == .alertFirstButtonReturn) }
    }
}
let app = NSApplication.shared
app.setActivationPolicy(.regular)
let delegate = AppDelegate()
app.delegate = delegate
app.run()

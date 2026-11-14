import QtQuick
import QtQuick.Layouts
import Quickshell
import Quickshell.Io

Item {
    id: root
    implicitWidth: pill.implicitWidth
    implicitHeight: pill.implicitHeight

    property int actionCount: 0
    property int pendingProposals: 0
    property string topHeadline: ""

    readonly property string summaryPath: (Quickshell.env("XDG_STATE_HOME") || (Quickshell.env("HOME") + "/.local/state")) + "/omathunder/bar-summary.json"

    Timer {
        interval: 3000
        running: true
        repeat: true
        triggeredOnStart: true
        onTriggered: summaryProc.running = true
    }

    Process {
        id: summaryProc
        command: ["sh", "-c", "cat \"$1\" 2>/dev/null || echo '{}'", "_", root.summaryPath]
        stdout: SplitParser {
            splitMarker: ""
            onRead: (data) => {
                try {
                    const obj = JSON.parse(data);
                    root.actionCount = obj.actionCount || 0;
                    root.pendingProposals = obj.pendingProposals || 0;
                    root.topHeadline = obj.topHeadline || "";
                } catch (e) {}
            }
        }
    }

    Process {
        id: launchTb
        command: ["sh", "-c", "hyprctl dispatch focuswindow class:thunderbird || thunderbird &"]
    }

    Rectangle {
        id: pill
        implicitWidth: row.implicitWidth + 14
        implicitHeight: 24
        radius: 6
        color: root.actionCount > 0 ? "#24283b" : "transparent"
        border.width: root.actionCount > 0 ? 1 : 0
        border.color: "#7aa2f7"

        RowLayout {
            id: row
            anchors.centerIn: parent
            spacing: 6
            Text {
                text: "󰇮"
                color: root.actionCount > 0 ? "#7aa2f7" : "#c0caf5"
                font.pixelSize: 13
            }
            Text {
                text: root.actionCount > 0 ? String(root.actionCount) : "0"
                color: "#c0caf5"
                font.pixelSize: 12
                font.bold: root.actionCount > 0
            }
            Text {
                visible: root.pendingProposals > 0
                text: "✦" + root.pendingProposals
                color: "#e0af68"
                font.pixelSize: 11
            }
            Text {
                visible: hoverArea.containsMouse && root.topHeadline !== ""
                text: root.topHeadline
                color: "#c0caf5"
                font.pixelSize: 11
                elide: Text.ElideRight
                Layout.maximumWidth: 260
            }
        }

        MouseArea {
            id: hoverArea
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onClicked: launchTb.running = true
        }
    }
}

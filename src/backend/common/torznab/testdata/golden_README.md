# Golden 文件生成依据

本目录为 SPEC-03 §3.3 的 golden 对拍样例。当前环境无法现场运行上游
Jackett 0.24.2900，以下 golden 文件**依据上游源码行为手写**
（`TorznabCapabilities.cs` GetXDocument、`ResultPage.cs` ToXml、
`ResultsController.cs` CreateErrorXML），逐元素对照生成；后续若取得上游
运行时输出，应以运行时快照替换本目录文件并复跑对拍测试。

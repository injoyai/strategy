import React, { useEffect, useRef, useState } from 'react'
import { Card, Table, Space, Input, message, Row, Col, Button, Switch, Tag, Popconfirm, Modal, Form, Tooltip } from 'antd'
import Editor from '@monaco-editor/react'
import { getStrategyAll, createStrategy, updateStrategy, setStrategyEnable, deleteStrategy } from '../lib/api'
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import { MonacoLanguageClient } from 'monaco-languageclient'
import { toSocket, WebSocketMessageReader, WebSocketMessageWriter } from 'vscode-ws-jsonrpc'
import { MonacoVscodeApiWrapper } from 'monaco-languageclient/vscodeApiWrapper'
import { configureDefaultWorkerFactory } from 'monaco-languageclient/workerFactory'
import { registerExtension, ExtensionHostKind } from '@codingame/monaco-vscode-api/extensions'
import '@codingame/monaco-vscode-theme-defaults-default-extension'
import * as vscode from 'vscode'

let themeDefaultsRegistered = false

export default function StrategyPage() {
  const [strategies, setStrategies] = useState<{ name: string, script?: string, enable?: boolean, package?: string }[]>([])
  const [scriptName, setScriptName] = useState<string>('')
  const [scriptCode, setScriptCode] = useState<string>('')
  const [newVisible, setNewVisible] = useState(false)
  const [newForm] = Form.useForm()
  const [isLspReady, setIsLspReady] = useState(false)
  const editorRef = useRef<any>(null)
  const clientRef = useRef<MonacoLanguageClient | null>(null)
  const socketRef = useRef<WebSocket | null>(null)
  const retryRef = useRef<any>(null)
  const versionRef = useRef(1)
  const changeDisposableRef = useRef<any>(null)

  // 使用固定类型名，避免生成不期望的类型名
  const FixedTypeName = 'Strategy'

  // 定义 Action 枚举值
  const CloseAction = { DoNotRestart: 1, Restart: 2 }
  const ErrorAction = { Continue: 1, Shutdown: 2 }

  useEffect(() => {
    const initLsp = async () => {
      try {
        const wrapper = new MonacoVscodeApiWrapper({
          $type: 'extended',
          viewsConfig: {
            $type: 'EditorService'
          },
          monacoWorkerFactory: configureDefaultWorkerFactory
        })
        await wrapper.start()
        setIsLspReady(true)
      } catch (e) {
        console.error('LSP Init Failed:', e)
        setIsLspReady(true)
      }
    }
    initLsp()
  }, [])

  const scheduleRetry = () => {
    if (retryRef.current) return
    retryRef.current = setTimeout(() => {
      retryRef.current = null
      startLsp()
    }, 1500)
  }

  const startLsp = () => {
    if (!isLspReady) {
      scheduleRetry()
      return
    }
    if (clientRef.current) return
    if (!editorRef.current) {
      scheduleRetry()
      return
    }
    const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws'
    const url = `${protocol}://${window.location.host}/api/lsp`
    const webSocket = new WebSocket(url)
    socketRef.current = webSocket

    webSocket.onopen = () => {
      const socket = toSocket(webSocket)
      const reader = new WebSocketMessageReader(socket)
      const writer = new WebSocketMessageWriter(socket)

      const languageClient = new MonacoLanguageClient({
        name: 'Go Language Client',
        clientOptions: {
          documentSelector: [{ language: 'go', scheme: 'file' }],
          workspaceFolder: {
            uri: vscode.Uri.parse('file:///workspace'),
            name: 'workspace',
            index: 0
          },
          errorHandler: {
            error: () => ({ action: ErrorAction.Continue }),
            closed: () => ({ action: CloseAction.DoNotRestart })
          }
        },
        messageTransports: { reader, writer }
      })
      clientRef.current = languageClient
      languageClient.start()
      languageClient.onReady().then(() => {
        const editor = editorRef.current
        const model = editor?.getModel()
        if (!model) return
        const uri = model.uri?.toString?.() || 'file:///workspace/main.go'
        const languageId = model.getLanguageId?.() || 'go'
        languageClient.sendNotification('textDocument/didOpen', {
          textDocument: {
            uri,
            languageId,
            version: versionRef.current,
            text: model.getValue()
          }
        })
        if (changeDisposableRef.current?.dispose) {
          changeDisposableRef.current.dispose()
        }
        changeDisposableRef.current = editor.onDidChangeModelContent(() => {
          versionRef.current += 1
          languageClient.sendNotification('textDocument/didChange', {
            textDocument: {
              uri,
              version: versionRef.current
            },
            contentChanges: [{ text: model.getValue() }]
          })
        })
      })
    }

    webSocket.onerror = () => {
      socketRef.current = null
      clientRef.current = null
      scheduleRetry()
    }

    webSocket.onclose = () => {
      socketRef.current = null
      clientRef.current = null
      scheduleRetry()
    }
  }

  useEffect(() => {
    startLsp()
    return () => {
      try { clientRef.current?.stop() } catch {}
      clientRef.current = null
      try { socketRef.current?.close() } catch {}
      socketRef.current = null
      if (changeDisposableRef.current?.dispose) {
        changeDisposableRef.current.dispose()
      }
      changeDisposableRef.current = null
      if (retryRef.current) {
        clearTimeout(retryRef.current)
        retryRef.current = null
      }
    }
  }, [isLspReady])

  const handleEditorDidMount = (editor: any, monaco: any) => {
    editorRef.current = editor
    const model = editor.getModel()
    if (!model || model.uri?.scheme !== 'file') {
      const uri = monaco.Uri.parse('file:///workspace/main.go')
      const nextModel = monaco.editor.createModel(scriptCode || '', 'go', uri)
      editor.setModel(nextModel)
    }
    startLsp()
  }



  async function loadList() {
    try {
      const list = await getStrategyAll()
      setStrategies(list)
      // 保持初始为空，不自动选中或填充编辑器内容
      return list
    } catch (e: any) {
      message.error(e?.message || '获取策略列表失败')
      setStrategies([])
      return []
    }
  }

  useEffect(() => {
    loadList()
  }, [])

  return (
    <Space direction="vertical" style={{ width: '100%' }} size="large">
      <Card title="策略">
        <Row gutter={[16, 16]}>
          <Col xs={24} md={6}>
          <Card
            size="small"
            title="策略列表"
            extra={
              <Space>
                <Tooltip title="新建">
                  <Button size="small" shape="circle" icon={<PlusOutlined />} onClick={() => { newForm.resetFields(); setNewVisible(true) }} />
                </Tooltip>
                <Tooltip title="刷新">
                  <Button size="small" shape="circle" icon={<ReloadOutlined />} onClick={async () => { await loadList(); message.success('已刷新') }} />
                </Tooltip>
              </Space>
            }
          >
            <Table
              size="small"
              rowKey="name"
              dataSource={strategies}
              pagination={{ pageSize: 10 }}
              rowClassName={(record) => record.name === scriptName ? 'ant-table-row-selected' : ''}
              onRow={r => ({ onClick: () => {
                const name = (r as any).name
                const script = (r as any).script || ''
                setScriptName(name)
                setScriptCode(script)
              } })}
              columns={[
                { title: '名称', dataIndex: 'name' },
                {
                  title: '启用',
                  dataIndex: 'enable',
                  render: (v: boolean, r: any) => (
                    <Switch
                      checked={v}
                      checkedChildren="启用"
                      unCheckedChildren="停用"
                      onChange={async (checked) => {
                        try {
                          await setStrategyEnable({ name: r.name, enable: checked })
                          message.success(checked ? '已启用' : '已停用')
                          await loadList()
                        } catch (e: any) {
                          message.error(e?.message || '操作失败')
                        }
                      }}
                    />
                  )
                },
                {
                  title: '操作',
                  render: (_: any, r: any) => (
                    <Popconfirm
                      title="确认删除该策略？"
                      onConfirm={async () => {
                        try {
                          await deleteStrategy(r.name)
                          message.success('删除成功')
                          if (scriptName === r.name) {
                            setScriptCode('')
                          }
                          await loadList()
                        } catch (e: any) {
                          message.error(e?.message || '删除失败')
                        }
                      }}
                    >
                      <Button size="small" danger>删除</Button>
                    </Popconfirm>
                  )
                },
              ]}
            />
            
          </Card>
          </Col>
          <Col xs={24} md={18}>
          <Card
            size="small"
            title={scriptName ? `编辑：${scriptName}` : '编辑器'}
            extra={
              <Space>
                <Button size="small" type="primary" onClick={async () => {
                  if (!scriptName) { message.warning('请选择策略'); return }
                  try {
                    const exists = strategies.find(s => s.name === scriptName)
                    const content = String(scriptCode || '')
                    const lines = content.split(/\r?\n/)
                    const first = lines[0] || ''
                    const isPkg = first.trim().toLowerCase().startsWith('package ')
                    const script = isPkg ? lines.slice(1).join('\n') : content
                    if (exists) {
                      await updateStrategy({ name: scriptName, script })
                      message.success('更新成功')
                    } else {
                      await createStrategy({ name: scriptName, script, enable: false })
                      message.success('创建成功')
                    }
                    await loadList()
                  } catch (e: any) {
                    message.error(e?.message || '保存失败')
                  }
                }}>保存</Button>
              </Space>
            }
          >
            <div style={{ height: '70vh' }}>
              <Editor
                height="100%"
                path="main.go" 
                defaultLanguage="go"
                theme="vs-dark"
                value={scriptCode}
                onChange={(v) => setScriptCode(v || '')}
                onMount={handleEditorDidMount}
                options={{
                  fontSize: 14,
                  minimap: { enabled: false },
                  scrollBeyondLastLine: false,
                  automaticLayout: true,
                }}
              />
            </div>
          </Card>
          </Col>
        </Row>
        <Modal
          title="新建策略"
          open={newVisible}
          onCancel={() => setNewVisible(false)}
          onOk={async () => {
            try {
              const v = await newForm.validateFields()
              const n = String(v.name || '').trim()
              if (!n) { message.warning('请输入策略名称'); return }
              const exists = strategies.find(s => s.name === n)
              if (exists) { message.warning('该名称已存在'); return }
              const tpl = `type ${FixedTypeName} struct{}

func (${FixedTypeName}) Name() string { return "${n}" }

func (${FixedTypeName}) Signals(ks protocol.Klines) []int {
	if len(ks) == 0 { return nil }
	out := make([]int, len(ks))
	for i := range ks {
		out[i] = 0
	}
	return out
}
`
              const script = tpl
              await createStrategy({ name: n, script: script, enable: false })
              message.success('创建成功')
              const latest = await loadList()
              const cur = latest.find(s => s.name === n)
              setScriptName(n)
              setScriptCode(cur?.script || '')
              setNewVisible(false)
            } catch (e: any) {
              if (e?.errorFields) return
              message.error(e?.message || '创建失败')
            }
          }}
        >
          <Form form={newForm} layout="vertical">
            <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]}>
              <Input placeholder="输入策略名称" />
            </Form.Item>
          </Form>
        </Modal>
      </Card>
    </Space>
  )
}

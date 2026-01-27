import React, { useEffect, useRef, useState } from 'react'
import { Card, Table, Space, Input, message, Row, Col, Button, Switch, Tag, Popconfirm, Modal, Form, Tooltip } from 'antd'
import { getStrategyAll, createStrategy, updateStrategy, setStrategyEnable, deleteStrategy } from '../lib/api'
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons'

export default function StrategyPage() {
  const [strategies, setStrategies] = useState<{ name: string, script?: string, enable?: boolean, package?: string }[]>([])
  const [scriptName, setScriptName] = useState<string>('')
  const [scriptCode, setScriptCode] = useState<string>('')
  const [newVisible, setNewVisible] = useState(false)
  const [newForm] = Form.useForm()
  const editorRef = useRef<any>(null)
  const gutterRef = useRef<HTMLDivElement | null>(null)

  // 使用固定类型名，避免生成不期望的类型名
  const FixedTypeName = 'Strategy'

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
              <style>{`
                .strategy-editor-gutter::-webkit-scrollbar,
                .strategy-editor-textarea::-webkit-scrollbar {
                  width: 0;
                  height: 0;
                }
              `}</style>
              <div style={{ height: '100%', display: 'flex', border: '1px solid #303030', borderRadius: 6, overflow: 'hidden', background: '#2b2b2b' }}>
                <div
                  ref={gutterRef}
                  className="strategy-editor-gutter"
                  style={{
                    background: '#242424',
                    color: '#9e9e9e',
                    padding: '8px 6px',
                    textAlign: 'right',
                    userSelect: 'none',
                    overflow: 'hidden',
                    scrollbarWidth: 'none',
                    msOverflowStyle: 'none'
                  }}
                >
                  <pre style={{ margin: 0, fontSize: 13, lineHeight: '20px', overflow: 'hidden', height: '100%' }}>
                    {Array.from({ length: (scriptCode.match(/\n/g)?.length || 0) + 1 }, (_, i) => i + 1).join('\n')}
                  </pre>
                </div>
                <Input.TextArea
                  ref={editorRef}
                  className="strategy-editor-textarea"
                  value={scriptCode}
                  onChange={(e) => setScriptCode(e.target.value)}
                  onScroll={(e) => {
                    if (gutterRef.current) {
                      gutterRef.current.scrollTop = e.currentTarget.scrollTop
                    }
                  }}
                  onKeyDown={(e) => {
                    if (e.key !== 'Tab') return
                    e.preventDefault()
                    const el = editorRef.current?.resizableTextArea?.textArea
                    if (!el) return
                    const start = el.selectionStart || 0
                    const end = el.selectionEnd || 0
                    const next = scriptCode.slice(0, start) + '\t' + scriptCode.slice(end)
                    setScriptCode(next)
                    requestAnimationFrame(() => {
                      el.selectionStart = start + 1
                      el.selectionEnd = start + 1
                    })
                  }}
                  style={{
                    height: '100%',
                    fontFamily: 'Consolas, Menlo, Monaco, monospace',
                    fontSize: 13,
                    lineHeight: '20px',
                    border: 'none',
                    borderRadius: 0,
                    background: '#2b2b2b',
                    color: '#f0f0f0',
                    scrollbarWidth: 'none',
                    msOverflowStyle: 'none',
                    resize: 'none'
                  }}
                  autoSize={false}
                />
              </div>
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

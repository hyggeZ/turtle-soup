import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { Validator } from '@seriousme/openapi-schema-validator'
import Ajv2020 from 'ajv/dist/2020.js'
import addFormats from 'ajv-formats'

const spec = readJSON(new URL('./openapi.json', import.meta.url))
const fixture = new URL('../../internal/room/testdata/snapshot-contract-v1.json', import.meta.url)
const actualPath = process.env.SNAPSHOT_CONTRACT_OUTPUT
const componentID = 'urn:turtle-soup:openapi'

function readJSON(path) {
  return JSON.parse(readFileSync(path, 'utf8'))
}

function makeValidator() {
  const ajv = new Ajv2020({ allErrors: true, strictSchema: false })
  addFormats(ajv)
  // JavaScript 只精确表示安全整数，超出范围的样例不能可靠地比较。
  ajv.addFormat('int64', { type: 'number', validate: Number.isSafeInteger })
  return ajv
}

function schemaValidator(ajv, schema) {
  const { $ref, ...contents } = schema
  return ajv.compile($ref ? { ...contents, $ref: `${componentID}${$ref}` } : contents)
}

test('接口文档符合 OpenAPI 3.1 规范，内部引用可以解析', async () => {
  const validator = new Validator()
  const result = await validator.validate(spec)
  assert.equal(result.valid, true, JSON.stringify(result.errors))
  assert.equal(validator.version, '3.1')

  const api = makeRoomValidator()
  for (const name of Object.keys(spec.components.schemas)) {
    api.compile({ $ref: `#/components/schemas/${name}` })
  }
})

test('规范本身缺失必填元数据时失败', async () => {
  const changed = structuredClone(spec)
  delete changed.info.version
  const result = await new Validator().validate(changed)
  assert.equal(result.valid, false)
  assert.ok(result.errors.some(error => error.instancePath === '/info' && error.params.missingProperty === 'version'))
})

function makeRoomValidator() {
  const ajv = makeValidator()
  // 把组件注册为内存文档，所有组件引用保持原来的 JSON Pointer。
  ajv.addSchema({ components: spec.components }, componentID)
  const compile = schema => schemaValidator(ajv, schema)
  const operation = spec.paths['/api/rooms/{id}'].get
  const params = new Map(operation.parameters.map(({ name, schema, in: place }) => {
    const type = schema.$ref ? spec.components.schemas[schema.$ref.split('/').at(-1)].type : schema.type
    return [name, { type, place, verify: compile(schema) }]
  }))
  const responses = new Map(Object.entries(operation.responses).map(([code, entry]) =>
    [Number(code), compile(entry.content['application/json'].schema)]))

  function verifyCase({ query, status, response }) {
    const values = new URLSearchParams(query)
    // 参数分别校验。历史与增量的组合规则由实际 400 响应和 Go 契约用例验证。
    for (const name of new Set(values.keys())) {
      const param = params.get(name)
      assert.equal(param?.place, 'query', `文档未声明查询参数 ${name}`)
      assert.equal(values.getAll(name).length, 1, `查询参数 ${name} 重复`)
      const raw = values.get(name)
      const value = param.type === 'integer' && /^[+-]?\d+$/.test(raw) ? Number(raw) : raw
      assert.equal(param.verify(value), true, `参数 ${name} ${ajv.errorsText(param.verify.errors)}`)
    }

    const verify = responses.get(status)
    assert.ok(verify, `文档未声明状态 ${status}`)
    assert.equal(verify(response), true, `HTTP ${status} 响应 ${ajv.errorsText(verify.errors)}`)
  }

  return { compile, verifyCase }
}

test('实际房间快照和错误响应符合文档', {
  skip: actualPath ? false : '未设置 SNAPSHOT_CONTRACT_OUTPUT，仅验证 OpenAPI 文档'
}, async t => {
  const expected = readJSON(fixture)
  const actual = readJSON(actualPath)
  assert.equal(actual.version, expected.version)
  const keys = ({ name, query, status }) => ({ name, query, status })
  assert.deepEqual(actual.cases.map(keys), expected.cases.map(keys), 'Go 实际响应必须覆盖全部共享样例')

  const { verifyCase } = makeRoomValidator()
  for (const scenario of actual.cases) {
    await t.test(scenario.name, () => verifyCase(scenario))
  }

  const success = actual.cases.find(scenario => scenario.status === 200)
  assert.ok(success, '需要用于反证的真实成功响应')
  await t.test('拒绝将数字游标改成字符串', () => {
    const response = { ...success.response, eventCursor: String(success.response.eventCursor) }
    assert.throws(() => verifyCase({ ...success, response }), /eventCursor.*integer/)
  })
  await t.test('拒绝丢失必填字段', () => {
    const response = { ...success.response }
    delete response.events
    assert.throws(() => verifyCase({ ...success, response }), /required property 'events'/)
  })
  await t.test('拒绝未声明的状态码', () => {
    assert.throws(() => verifyCase({ ...success, status: 418 }), /文档未声明状态 418/)
  })
  await t.test('拒绝非整数查询参数', () => {
    const query = new URLSearchParams(success.query)
    query.set('after', '1e3')
    assert.throws(() => verifyCase({ ...success, query: query.toString() }), /参数 after.*integer/)
  })
})

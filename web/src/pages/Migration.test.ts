import { describe, it, expect } from "vitest"
import { parseCSVRows, parseRosterCSV, ParseError } from "./Migration"

describe("parseCSVRows", () => {
  it("простой CSV с CRLF и LF", () => {
    expect(parseCSVRows("a,b,c\r\n1,2,3\nx,y,z")).toEqual([
      ["a", "b", "c"],
      ["1", "2", "3"],
      ["x", "y", "z"],
    ])
  })

  it("кавычки прячут запятую и перевод строки внутри поля", () => {
    expect(parseCSVRows('name,note\r\n"Doe, John","line1\nline2"')).toEqual([
      ["name", "note"],
      ["Doe, John", "line1\nline2"],
    ])
  })

  it("экранированная кавычка \"\" → одна кавычка", () => {
    expect(parseCSVRows('a\r\n"say ""hi"""')).toEqual([["a"], ['say "hi"']])
  })

  it("пустые строки выкидываются, а пустые поля внутри строки — нет", () => {
    expect(parseCSVRows("a,b\r\n\r\n1,\r\n\r\n")).toEqual([
      ["a", "b"],
      ["1", ""],
    ])
  })

  it("одиночные \\r (classic-Mac) тоже завершают строку, а не схлопывают файл", () => {
    expect(parseCSVRows("a,b\rc,d\re,f")).toEqual([
      ["a", "b"],
      ["c", "d"],
      ["e", "f"],
    ])
    // смешанные окончания в одном файле не должны дробить/склеивать
    expect(parseCSVRows("a,b\r\nc,d\re,f\n")).toEqual([
      ["a", "b"],
      ["c", "d"],
      ["e", "f"],
    ])
  })
})

describe("parseRosterCSV", () => {
  it("матчит колонки по синонимам заголовка (регистронезависимо)", () => {
    const csv = "Computer Name,Serial Number,Primary User\nPC-1,SN-1,alice@corp\nPC-2,SN-2,bob@corp"
    expect(parseRosterCSV(csv)).toEqual([
      { hostname: "PC-1", serial_number: "SN-1", assigned_user: "alice@corp", asset_tag: "", group_hint: "", notes: "" },
      { hostname: "PC-2", serial_number: "SN-2", assigned_user: "bob@corp", asset_tag: "", group_hint: "", notes: "" },
    ])
  })

  it("порядок и набор колонок произвольные, лишние игнорируются", () => {
    const csv = "asset tag,hostname,department,unknowncol\nTAG-9,PC-9,Sales,junk"
    expect(parseRosterCSV(csv)).toEqual([
      { hostname: "PC-9", serial_number: "", assigned_user: "", asset_tag: "TAG-9", group_hint: "Sales", notes: "" },
    ])
  })

  it("строки без hostname и serial отбрасываются", () => {
    const csv = "hostname,serial,notes\nPC-1,,\n,,only-a-note\n,,"
    expect(parseRosterCSV(csv)).toEqual([
      { hostname: "PC-1", serial_number: "", assigned_user: "", asset_tag: "", group_hint: "", notes: "" },
    ])
  })

  it("значения трогаются trim-ом", () => {
    const csv = "hostname, serial \n  PC-1  , SN-1 "
    const rows = parseRosterCSV(csv)
    expect(rows[0].hostname).toBe("PC-1")
    expect(rows[0].serial_number).toBe("SN-1")
  })

  it("нет ни hostname, ни serial в заголовке → ParseError no-column", () => {
    expect(() => parseRosterCSV("email,department\na@b,Sales")).toThrow(ParseError)
    try {
      parseRosterCSV("email,department\na@b,Sales")
    } catch (e) {
      expect((e as ParseError).code).toBe("no-column")
    }
  })

  it("голая колонка «Name» (ФИО в AD/Entra-выгрузке) НЕ считается hostname → no-column", () => {
    // Регресс: жадный синоним "name" молча уводил ФИО сотрудника в hostname, минуя
    // проверку «нет колонки», и весь ростер оказывался мусором без единой ошибки.
    try {
      parseRosterCSV("Name,Email,Department\nJane Smith,jane@corp,Sales")
      throw new Error("should have thrown no-column")
    } catch (e) {
      expect((e as ParseError).code).toBe("no-column")
    }
  })

  it("«Computer Name» рядом с «Name» — hostname берётся из computer name, не из name", () => {
    const rows = parseRosterCSV("Name,Serial Number,Computer Name\nJane,SN-1,PC-1")
    expect(rows[0].hostname).toBe("PC-1")
    expect(rows[0].serial_number).toBe("SN-1")
  })

  it("пустой файл → ParseError empty", () => {
    try {
      parseRosterCSV("")
      throw new Error("should have thrown")
    } catch (e) {
      expect((e as ParseError).code).toBe("empty")
    }
  })

  it("одного serial без hostname достаточно (сильный ключ матча)", () => {
    const csv = "serial number\nSN-ONLY"
    expect(parseRosterCSV(csv)).toEqual([
      { hostname: "", serial_number: "SN-ONLY", assigned_user: "", asset_tag: "", group_hint: "", notes: "" },
    ])
  })
})

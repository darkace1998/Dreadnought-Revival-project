#!/usr/bin/env python3
"""Render bpdump's JSON bytecode disassembly as one line of pseudo-code per
statement.

  bpcompact.py <file.dis> [function-regex]
"""
import json, re, sys


def short(s):
    s = str(s)
    s = re.sub(r'_\d+_[0-9A-F]{32}', '', s)       # struct member GUID suffix
    s = re.sub(r'^/(Script|Game)/[^.]*\.', '', s)  # package path
    return s.replace('Default__', '')


def r(e):
    if e is None:
        return '∅'
    if isinstance(e, list):
        return ', '.join(r(x) for x in e)
    if not isinstance(e, dict):
        return short(e)
    i = e.get('Inst', '?')
    if i in ('LocalVariable', 'InstanceVariable', 'LocalOutVariable', 'DefaultVariable'):
        n = short(e.get('Variable Name', '?'))
        n = n.split('.', 1)[1] if '.' in n and i != 'InstanceVariable' else n
        return ('self.' if i == 'InstanceVariable' else '') + n
    if i in ('Let', 'LetBool', 'LetObj', 'LetWeakObjPtr', 'LetDelegate', 'LetMulticastDelegate'):
        return f"{r(e.get('Variable'))} = {r(e.get('Expression'))}"
    if i in ('CallMath', 'FinalFunction', 'VirtualFunction', 'LocalFinalFunction', 'LocalVirtualFunction'):
        return f"{short(e.get('Function'))}({r(e.get('Parameters', []))})"
    if i == 'Context' or i == 'Context_FailSilent':
        return f"{r(e.get('Context'))}.{r(e.get('Expression'))}"
    if i == 'StructMemberContext':
        return f"{r(e.get('StructExpression'))}.{short(e.get('Property Name', '?')).split('.')[-1]}"
    if i in ('IntConst', 'FloatConst', 'ByteConst', 'IntConstByte', 'NameConst', 'StringConst',
             'UnicodeStringConst', 'TextConst', 'ObjectConst'):
        v = e.get('Value', e.get('Object', e.get('Name')))
        return repr(v) if 'String' in i or 'Name' in i else short(v)
    if i in ('True', 'False', 'NoObject', 'Self', 'IntZero', 'IntOne', 'Nothing', 'Return'):
        if i == 'Return':
            return f"return {r(e.get('Expression'))}" if e.get('Expression') else 'return'
        return {'True': 'true', 'False': 'false', 'NoObject': 'None', 'Self': 'self', 'IntZero': '0',
                'IntOne': '1', 'Nothing': ''}[i]
    if i == 'Jump':
        return f"goto {e.get('Offset', e.get('CodeOffset'))}"
    if i == 'JumpIfNot':
        return f"if not ({r(e.get('Condition'))}) goto {e.get('Offset', e.get('CodeOffset'))}"
    if i in ('PushExecutionFlow',):
        return f"push {e.get('Offset', e.get('PushingAddress'))}"
    if i in ('PopExecutionFlow', 'PopExecutionFlowIfNot'):
        return i + (f" ({r(e.get('Condition'))})" if e.get('Condition') else '')
    if i in ('DynamicCast', 'MetaCast', 'ObjToInterfaceCast', 'CrossInterfaceCast', 'InterfaceToObjCast'):
        return f"Cast<{short(e.get('Class', e.get('InterfaceClass', '?')))}>({r(e.get('Target', e.get('Expression')))})"
    if i == 'ComputedJump':
        return f"goto computed {r(e.get('OffsetExpression'))}"
    if i in ('AddMulticastDelegate', 'RemoveMulticastDelegate', 'BindDelegate'):
        return f"{i}({r(e.get('Delegate'))}, {r(e.get('DelegateToAdd', e.get('Name')))})"
    if i == 'InstanceDelegate':
        return f"delegate {e.get('FunctionName')}"
    if i == 'CallMulticastDelegate':
        return f"broadcast {short(e.get('Delegate', e.get('FunctionName')))}({r(e.get('Parameters', []))})"
    if i in ('SetArray', 'ArrayConst'):
        return f"[{r(e.get('Elements', e.get('Values', [])))}]"
    if i in ('StructConst',):
        return f"{short(e.get('Struct', ''))}{{{r(e.get('Value', e.get('Properties', [])))}}}"
    if i == 'SwitchValue':
        return f"switch({r(e.get('IndexTerm'))})"
    rest = {k: v for k, v in e.items() if k not in ('Inst', 'StatementIndex', 'SkipOffsetForNull',
                                                       'RValuePropertyOuter', 'RValuePropertyName')}
    return f"{i}<{', '.join(f'{k}={r(v)}' for k, v in rest.items())}>"


def main():
    text = open(sys.argv[1]).read()
    pat = re.compile(sys.argv[2]) if len(sys.argv) > 2 else None
    for m in re.finditer(r'^==== FUNCTION (\S+) .*?====\n(\[.*?^\])', text, re.M | re.S):
        name = m.group(1)
        if pat and not pat.search(name):
            continue
        print(f'== {name}')
        for st in json.loads(m.group(2)):
            print(f"{st.get('StatementIndex', '?'):>6}: {r(st)}")


if __name__ == '__main__':
    main()

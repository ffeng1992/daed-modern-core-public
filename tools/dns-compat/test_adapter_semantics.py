import unittest,asyncio,struct
from unittest.mock import patch
import importlib.util, os
if os.environ.get('SITE_ADAPTER_PATH'):
 spec=importlib.util.spec_from_file_location('site_gate',os.environ['SITE_ADAPTER_PATH'])
 m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
else:m=None
def query(name='example.com',typ=1):
 return b'\x12\x34\x01\x00\x00\x01'+b'\0'*6+b''.join(bytes([len(x)])+x.encode() for x in name.split('.'))+b'\0'+struct.pack('!HH',typ,1)
def reply(q,rcode=0,answers=0):return q[:2]+bytes([0x81,0x80|rcode])+q[4:6]+answers.to_bytes(2,'big')+b'\0'*4+q[12:]
@unittest.skipUnless(m, 'requires reviewed site fixture')
class Gate(unittest.IsolatedAsyncioTestCase):
 async def test_positive_preserves_core_response(self):
  q=query();r=reply(q,answers=1)
  with patch.object(m,'upstream',return_value=r) as f:
   self.assertEqual(await m.gate(q),r);self.assertEqual(f.call_args.args[1],5353)
 async def test_empty_verified_nxdomain(self):
  q=query();a=reply(q);b=reply(q,3)
  with patch.object(m,'upstream',side_effect=[a,b]):self.assertEqual(await m.gate(q),b)
 async def test_nodata_not_falsely_nxdomain(self):
  q=query();r=reply(q)
  with patch.object(m,'upstream',side_effect=[r,r]):self.assertEqual(await m.gate(q),r)
 async def test_no_positive_bypass_of_core_cache(self):
  q=query();r=reply(q)
  with patch.object(m,'upstream',side_effect=[r,reply(q,answers=1)]):self.assertEqual(await m.gate(q),r)
 async def test_servfail_one_retry(self):
  q=query();r=reply(q,answers=1)
  with patch.object(m,'upstream',side_effect=[reply(q,2),r]) as f:
   self.assertEqual(await m.gate(q),r);self.assertEqual(f.call_count,2)
 async def test_timeout_returns_servfail(self):
  with patch.object(m,'upstream',side_effect=TimeoutError):self.assertEqual((await m.gate(query()))[3]&15,2)
 async def test_non_a_uses_semantic_path(self):
  q=query(typ=28);r=reply(q)
  with patch.object(m,'upstream',return_value=r) as f:
   self.assertEqual(await m.gate(q),r);self.assertEqual(f.call_args.args[1],5535)
if __name__=='__main__':unittest.main()

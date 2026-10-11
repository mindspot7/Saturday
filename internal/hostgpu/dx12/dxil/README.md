# Precompiled DX12 compute shaders

The DX12 backend compiles no compute shaders at run time. The compute packages
are lowered and emitted as HLSL under the build tree, compiled to DXIL
by the DirectX Shader Compiler, and the signed blobs written here are
embedded. There are three sets:

- renderer: the renderer's entry points.
- conformance: the conformance entries the device tests run.
- backend: the backend's own kernels (the fill), which every device
  needs at open.

Generate them with:

```
make dxil
```

This runs utils/dxil_gen, which needs dxc.exe on PATH (or DXC= naming
it) with dxil.dll beside it, so every blob is validated and signed. The HLSL sources are written under build/hlsl, one directory
per set, and only the blobs land here.

You can build the HLSL files only using:

```
go run ./utils/dxil_gen -dxc ""
```

If the dxil files are not precompiled, the DX12 backend
reports that and the renderer runs in software. The device tests
are skipped. Run `make dxil` again after any change to core/compute, then
rebuild. `make windows` runs both steps.

Topology presets for the demo cluster.

Each preset has:
- a `*.kind.yaml` file describing the kind node count
- a `*.labels.tsv` file describing node topology labels

The TSV format is:

`node_name<TAB>region<TAB>zone<TAB>rack`

Available presets:

- `simple-3node`
  - 3 workers
  - 2 workers in the same rack/zone, 1 worker remote
- `single-zone-3rack`
  - 3 workers
  - same zone, 3 different racks
- `three-zone-3worker`
  - 3 workers
  - one worker per zone
- `two-zone-4worker`
  - 4 workers
  - 2 workers per zone across 4 racks
- `three-zone-6worker`
  - 6 workers
  - 2 workers per zone across 6 racks

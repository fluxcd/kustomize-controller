#!/usr/bin/env bash

# Copyright 2022 The Flux authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euxo pipefail

# This file is executed by upstream oss-fuzz for any requirements that
# are specific for building this project.

# Some tests requires embedded resources. Embedding does not allow
# for traversing into ascending dirs, therefore we copy those contents here:
mkdir -p internal/controllers/testdata/crd
cp config/crd/bases/*.yaml internal/controllers/testdata/crd

cmake_minimum_required(VERSION 3.20)
project(jev-behavior-tree LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

include(FetchContent)
FetchContent_Declare(
  nlohmann_json
  URL https://github.com/nlohmann/json/archive/refs/tags/v3.11.3.zip
)
set(JSON_BuildTests OFF CACHE BOOL "" FORCE)
FetchContent_MakeAvailable(nlohmann_json)

add_library(jevlib
  src/policy.cpp
  src/roles.cpp
  src/engine.cpp
  src/judge.cpp
)
target_include_directories(jevlib PUBLIC src)
target_link_libraries(jevlib PUBLIC nlohmann_json::nlohmann_json)
if(MSVC)
  target_compile_options(jevlib PUBLIC /utf-8)
endif()
if(WIN32)
  target_link_libraries(jevlib PUBLIC winhttp ws2_32)
endif()

add_executable(jev src/main.cpp)
target_link_libraries(jev PRIVATE jevlib)

add_executable(jev-tests src/tests.cpp)
target_link_libraries(jev-tests PRIVATE jevlib)

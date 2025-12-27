<?php
    // require_once('../admin/dl.inc.php');
function get_bruno_home() {
  return realpath(dirname(__FILE__)."/..");
}

function errdie($msg, $status="500 Internal Server Error") {
  header("HTTP/1.1 $status");
  die($msg);
}


function is_utf8($str) {
  $strlen = strlen($str);
  for ($i = 0; $i < $strlen; $i++) {
    $ord = ord($str[$i]);
    if ($ord < 0x80) { continue; // 0bbbbbbb
    } elseif (($ord & 0xE0) === 0xC0 && $ord > 0xC1) { $n = 1; // 110bbbbb (exkl C0-C1)
    } elseif (($ord & 0xF0) === 0xE0) { $n = 2; // 1110bbbb
    } elseif (($ord & 0xF8) === 0xF0 && $ord < 0xF5) { $n = 3; // 11110bbb (exkl F5-FF)
    } else { return false; // invalid UTF-8-Zeichen
    }
    for ($c=0; $c<$n; $c++) { // $n following bytes? // 10bbbbbb
      if (++$i === $strlen || (ord($str[$i]) & 0xC0) !== 0x80) {
        return false; // invalid UTF-8 char
      }
    }
  }
  return true; // didn't find any invalid characters
}

/*
package main

import(
	"fmt"

	"golang.org/x/text/encoding/charmap"
)

func main() {
	for i := range 256 {
		if d := charmap.ISO8859_2.DecodeByte(byte(i)); d < 0xff {
			fmt.Printf("\"\\x%02x\", ", d)
		} else {
			fmt.Printf("\"\\u{%04x}\", ", d)
		}
		if i%8 == 7 {
			fmt.Println("")
		}
	}
}
*/
$_iso88592_utf8_table = array(
  "\x00", "\x01", "\x02", "\x03", "\x04", "\x05", "\x06", "\x07", 
  "\x08", "\x09", "\x0a", "\x0b", "\x0c", "\x0d", "\x0e", "\x0f", 
  "\x10", "\x11", "\x12", "\x13", "\x14", "\x15", "\x16", "\x17", 
  "\x18", "\x19", "\x1a", "\x1b", "\x1c", "\x1d", "\x1e", "\x1f", 
  "\x20", "\x21", "\x22", "\x23", "\x24", "\x25", "\x26", "\x27", 
  "\x28", "\x29", "\x2a", "\x2b", "\x2c", "\x2d", "\x2e", "\x2f", 
  "\x30", "\x31", "\x32", "\x33", "\x34", "\x35", "\x36", "\x37", 
  "\x38", "\x39", "\x3a", "\x3b", "\x3c", "\x3d", "\x3e", "\x3f", 
  "\x40", "\x41", "\x42", "\x43", "\x44", "\x45", "\x46", "\x47", 
  "\x48", "\x49", "\x4a", "\x4b", "\x4c", "\x4d", "\x4e", "\x4f", 
  "\x50", "\x51", "\x52", "\x53", "\x54", "\x55", "\x56", "\x57", 
  "\x58", "\x59", "\x5a", "\x5b", "\x5c", "\x5d", "\x5e", "\x5f", 
  "\x60", "\x61", "\x62", "\x63", "\x64", "\x65", "\x66", "\x67", 
  "\x68", "\x69", "\x6a", "\x6b", "\x6c", "\x6d", "\x6e", "\x6f", 
  "\x70", "\x71", "\x72", "\x73", "\x74", "\x75", "\x76", "\x77", 
  "\x78", "\x79", "\x7a", "\x7b", "\x7c", "\x7d", "\x7e", "\x7f", 
  "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", 
  "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", 
  "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", 
  "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", "\u{fffd}", 
  "\xa0", "\u{0104}", "\u{02d8}", "\u{0141}", "\xa4", "\u{013d}", "\u{015a}", "\xa7", 
  "\xa8", "\u{0160}", "\u{015e}", "\u{0164}", "\u{0179}", "\xad", "\u{017d}", "\u{017b}", 
  "\xb0", "\u{0105}", "\u{02db}", "\u{0142}", "\xb4", "\u{013e}", "\u{015b}", "\u{02c7}", 
  "\xb8", "\u{0161}", "\u{015f}", "\u{0165}", "\u{017a}", "\u{02dd}", "\u{017e}", "\u{017c}", 
  "\u{0154}", "\xc1", "\xc2", "\u{0102}", "\xc4", "\u{0139}", "\u{0106}", "\xc7", 
  "\u{010c}", "\xc9", "\u{0118}", "\xcb", "\u{011a}", "\xcd", "\xce", "\u{010e}", 
  "\u{0110}", "\u{0143}", "\u{0147}", "\xd3", "\xd4", "\u{0150}", "\xd6", "\xd7", 
  "\u{0158}", "\u{016e}", "\xda", "\u{0170}", "\xdc", "\xdd", "\u{0162}", "\xdf", 
  "\u{0155}", "\xe1", "\xe2", "\u{0103}", "\xe4", "\u{013a}", "\u{0107}", "\xe7", 
  "\u{010d}", "\xe9", "\u{0119}", "\xeb", "\u{011b}", "\xed", "\xee", "\u{010f}", 
  "\u{0111}", "\u{0144}", "\u{0148}", "\xf3", "\xf4", "\u{0151}", "\xf6", "\xf7", 
  "\u{0159}", "\u{016f}", "\xfa", "\u{0171}", "\xfc", "\xfd", "\u{0163}", "\u{02d9}",
);

function to_utf8_table( $str ) {
  global $_iso88592_utf8_table;
  $strlen = strlen($str);
  $t_text = '';
  for ($i = 0; $i < $strlen; $i++) {
    $ord = ord($str[$i]);
    $t_text .= $_iso88592_utf8_table[$ord];
  }
  return $t_text;
}

function to_utf8( $p_text ) {
  if( !$p_text ) {
    return $p_text;
  } 
  if( is_array( $p_text ) ) {
    foreach( $p_text as $k => $v ) {
      $p_text[$k] = to_utf8( $v );
    }
    return $p_text;
  }
  if( !is_string( $p_text ) ) {
    return $p_text;
  }
  if( function_exists( 'mb_detect_encoding' ) ) {
    if( mb_detect_encoding( $p_text, 'UTF-8', true ) ) {
      return $p_text;
    }
  } elseif( is_utf8( $p_text ) ) {
    return $p_text;
  }
  if( function_exists( 'mb_convert_encoding' ) ) {
    return mb_convert_encoding( $p_text, 'UTF-8', 'ISO-8859-2' );
  }
  // return to_utf8_table( $p_text );
  // phpinfo();
  return $p_text;
}

    $t_root = realpath( get_bruno_home() . '/data/mai/log' );
    if( ( $f_tail = $_GET['tail'] ) ) {
        $t_file = realpath( $t_root . '/' . $f_tail );
        if( !$t_file ) {
            errdie( "$t_file not found", '404 Not Found' );
        } else if ( $t_root != substr( $t_file, 0, strlen($t_root) ) ) {
            errdie( "$t_file is not under $t_root", '401 Access Denied' );
        }
        $t_cmd = 'tail -F ';
        if( filesize( $t_file ) != 0 ) {
            $t_output = null;
            $t_rc = 0;
            exec( 'gzip -t ' . escapeshellarg( $t_file ), $t_output, $t_rc );
            if( $t_rc == 0 ) {
                $t_pipe = gzopen( $t_file, 'r' );
                $t_cmd = null;
            }
        }
        if( $t_cmd ) {
            $t_cmd .= escapeshellarg( $t_file ) . ' 2>&1';
            $t_pipe = popen( $t_cmd, 'r' );
        }
        if( !$t_pipe ) {
            errdie( "$t_cmd: " . fgets( $t_pipe ) );
        }
        $f_left = $_GET['left'];
        $f_right = $_GET['right'];

        // https://kevinchoppin.dev/blog/server-sent-events-in-php
        // make session read-only
        session_start();
        session_write_close();

        // disable default disconnect checks
        ignore_user_abort(true);

        // set headers for stream
        header("Content-Type: text/event-stream; charset=utf-8");
        header("Cache-Control: no-cache");
        header("Connection: keep-alive");
        header("Access-Control-Allow-Origin: *");

        // dummy start for empty files
        $t_rest = '';
        $t_lines = array('');
        // start stream
        while( true ) {
            // echo "<!--" . var_export( $t_lines, TRUE ) . "-->\n";
            if( $f_left || $f_right ) {
                foreach( $t_lines as $t_line ) {
                // echo "<!--" . var_export( htmlentities($t_line), TRUE ) . "-->\n";
                // echo "<!--" . var_export( htmlspecialchars( $t_line ), TRUE ) . "-->\n";
                    echo "data: $f_left" . $t_line . "$f_right\n\n";
                }
            } else {
                foreach( $t_lines as $t_line ) {
                    echo "data: $t_line\n\n";
                }
            }
            ob_flush();
            flush();

            if( connection_aborted() ) {
                break;
            } 

            $t_line = fread( $t_pipe, 8192 );
            if( !$t_line && !$t_cmd ) {
                sleep(10);
            }
            $t_line = to_utf8( $t_rest . $t_line );
            $t_rest = $t_line;
            $i = strrpos( $t_line, "\n" );
            if( $i ) {
                $t_rest = substr( $t_line, $i + 1 );
                $t_line = substr( $t_line, 0, $i );
                $t_line = to_utf8( $t_line );
                $t_lines = explode( "\n", $t_line );
            }
        }
        if( $t_cmd ) {
            pclose( $t_pipe );
        } else {
            fclose( $t_pipe );
        }

    } elseif( ( $f_file = $_GET['file'] ) ) {

        header('Content-Type: text/html');
        ?><!DOCTYPE html>
<html>
    <head>
        <title>WebTail</title>
        
        <script src="https://cdn.jsdelivr.net/npm/htmx.org@2.0.8/dist/htmx.min.js" integrity="sha384-/TgkGk7p307TH7EXJDuUlgG3Ce1UVolAOFopFekQkkXihi5u/6OCvVKyz1W+idaz" crossorigin="anonymous"></script>
        <script src="https://cdn.jsdelivr.net/npm/htmx-ext-sse@2.2.4" integrity="sha384-A986SAtodyH8eg8x8irJnYUk7i9inVQqYigD6qZ9evobksGNIXfeFvDwLSHcp31N" crossorigin="anonymous"></script>
    </head>
    <body>
        <h1><?php echo htmlspecialchars( $f_file ); ?></h1>
        <pre hx-ext="sse" sse-connect="?left=&right=<br>&tail=<?php echo urlencode( $f_file ); ?>" sse-swap="message" hx-swap="beforeend swap:1s">
        </pre>
    </body>
</html><?php

    } else {

        $f_dir = $_GET['dir'];
        $t_dir = realpath( $t_root . '/' . $f_dir );
        if( !$t_dir ) {
            errdie( "dir $t_dir not found", '404 Not Found' );
        } else if ( $t_root != substr( $t_dir, 0, strlen($t_root) ) ) {
            errdie( "$t_dir is not under $t_root", '401 Access Denied' );
        }
        if( !($t_names = scandir( $t_dir ) ) ) {
            errdie( "cannot open $t_dir", '500 Internal Server Serror' );
        }

        header('Content-Type: text/html');
        ?><!DOCTYPE html>
<html>
    <head>
        <title>WebTail</title>
    </head>
<body>
    <p><ul>
        <?php 
        $t_bd = substr( $t_dir, strlen($t_root) + 1 );
        if( $t_bd ) {
            $t_bd .= '/';
        }
        $t_files = array();
        $t_mtimes = array();
        foreach( $t_names as $t_fn ) {
            $t_afn = $t_dir . '/' . $t_fn;
            if( is_dir( $t_afn ) ) {
                echo "<li><a href=\"?dir=" . urlencode( $t_bd . $t_fn ) . '">' . 
                    htmlspecialchars( $t_fn ) . "</a></li>\n";
            } elseif( is_file( $t_afn ) ) {
                $t_files[] = $t_fn;
                $t_mtimes[] = filemtime( $t_afn );
            } else {
                continue;
            }
        }
        array_multisort( $t_mtimes, SORT_DESC, SORT_NUMERIC, $t_files );
        foreach( $t_files as $i => $t_fn ) {
            $t_afn = $t_dir . '/' . $t_fn;
            echo "<li><a href=\"?file=" . urlencode( $t_bd . $t_fn ) . '">' . 
                htmlspecialchars( $t_fn ) . "</a> " . 
                date( "Y-m-d H:i:s", $t_mtimes[$i] ) . " " .
                human_filesize( filesize( $t_afn ) ) . "</li>\n";
        }
        ?>
    </ul></p>
</body>
</html><?php
    }
